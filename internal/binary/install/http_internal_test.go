package install

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	errSimulatedNetworkError = errors.New("simulated network error")
)

const (
	// testToken is a session token of tarantool.io.
	testToken      = "test-token"
	packageTestURL = "https://packages.test"
)

type prebuiltTestTransport func(*http.Request) (*http.Response, error)

func (transport prebuiltTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// packageTestClient handles requests in memory, including redirects and streamed bodies.
func packageTestClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()

	return &http.Client{Transport: prebuiltTestTransport(func(request *http.Request) (
		*http.Response, error,
	) {
		require.Equal(t, "packages.test", request.URL.Host)

		writer := httptest.NewRecorder()

		handler(writer, request)

		response := writer.Result()

		response.Request = request

		if !writer.Flushed && response.Header.Get("Content-Length") == "" {
			response.ContentLength = int64(writer.Body.Len())
		}

		return response, nil
	})}
}

// mockBundleDoer is a mock implementation of TntIoDoer for DownloadBundle tests.
type mockBundleDoer struct {
	t           *testing.T
	token       string
	resBody     []byte
	resErr      error
	expectedURL string
	// foreign is a source off tarantool.io, which the token is not sent to.
	foreign bool
}

// Do is not a part of a download.
func (m *mockBundleDoer) Do(req *http.Request) ([]byte, error) {
	m.t.Helper()
	m.t.Errorf("a download must stream %s", req.URL)

	return nil, errSimulatedNetworkError
}

// Download mocks the Download method of TntIoDoer.
// It verifies the request's URL, method, User-Agent header, and sessionid cookie.
func (m *mockBundleDoer) Download(req *http.Request, dst io.Writer) error {
	m.t.Helper()

	require.Equal(m.t, m.expectedURL, req.URL.String(), "Request URL mismatch")
	require.Equal(m.t, http.MethodGet, req.Method, "Request method mismatch")
	require.Equal(m.t, "tt", req.Header.Get("User-Agent"), "User-Agent header mismatch")

	if m.token != "" && !m.foreign {
		cookie, err := req.Cookie("sessionid")
		require.NoError(m.t, err, "sessionid cookie not found when token is non-empty")
		require.NotNil(m.t, cookie, "sessionid cookie is nil when token is non-empty")
		require.Equal(m.t, m.token, cookie.Value, "sessionid cookie value mismatch")
	} else {
		_, err := req.Cookie("sessionid")
		require.ErrorIs(m.t, err, http.ErrNoCookie,
			"sessionid cookie should be present only for tarantool.io with a token")
	}

	_, err := dst.Write(m.resBody)
	require.NoError(m.t, err)

	return m.resErr
}

// Token mocks the Token method of TntIoDoer.
func (m *mockBundleDoer) Token() string {
	return m.token
}

func TestDownloadBundle(t *testing.T) {
	defaultDstSetup := func(t *testing.T, dir string) string {
		t.Helper()

		return dir
	}

	tests := map[string]struct {
		bundleName   string
		bundleSource string
		dstSetup     func(t *testing.T, dir string) string
		doer         *mockBundleDoer
		errMsg       string
	}{
		"successful download with token": {
			bundleName:   "bundle_with_token.tar.gz",
			bundleSource: "https://www.tarantool.io/bundle_with_token.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:   testToken,
				resBody: []byte("bundle content with token"),
			},
		},

		"successful download no token": {
			bundleName:   "bundle_no_token.tar.gz",
			bundleSource: "https://www.tarantool.io/bundle_no_token.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:   "",
				resBody: []byte("bundle content no token"),
			},
		},

		"token is not sent off tarantool.io": {
			bundleName:   "bundle_foreign.tar.gz",
			bundleSource: "https://storage.test/bundle_foreign.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:   testToken,
				resBody: []byte("bundle content off tarantool.io"),
				foreign: true,
			},
		},

		"token is not sent over plain HTTP": {
			bundleName:   "bundle_http.tar.gz",
			bundleSource: "http://www.tarantool.io/bundle_http.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:   testToken,
				resBody: []byte("bundle content over HTTP"),
				foreign: true,
			},
		},

		"error from TntIoDoer": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "https://www.tarantool.io/bundle.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:   testToken,
				resBody: []byte("partial bundle"),
				resErr:  errSimulatedNetworkError,
			},
			errMsg: "simulated network error",
		},

		"nil TntIoDoer in searchCtx": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "https://www.tarantool.io/bundle.tar.gz",
			dstSetup:     defaultDstSetup,
			doer:         nil,
			errMsg:       "no tarantool.io doer was applied",
		},

		"destination directory does not exist": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "https://www.tarantool.io/bundle.tar.gz",
			dstSetup: func(t *testing.T, baseDir string) string {
				t.Helper()

				nonExistentDir := filepath.Join(baseDir, "non_existent_subdir_for_sure")

				return nonExistentDir
			},
			doer: &mockBundleDoer{
				token: testToken,
			},
			errMsg: "destination directory doesn't exist",
		},

		"destination is not a directory": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "https://www.tarantool.io/bundle.tar.gz",
			dstSetup: func(t *testing.T, baseDir string) string {
				t.Helper()

				file, err := os.CreateTemp(baseDir, "destination_as_file_*")
				require.NoError(t, err)

				filePath := file.Name()

				_ = file.Close()

				return filePath
			},
			doer: &mockBundleDoer{
				token: testToken,
			},
			errMsg: "destination path is not a directory",
		},

		"invalid bundleSource URL format": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "::invalid_url_format",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token: testToken,
			},
			errMsg: "failed to create HTTP request: " +
				`parse "::invalid_url_format": missing protocol scheme`,
		},

		"fails create file due to path conflict": {
			bundleName:   "conflict_dir/bundle.tar.gz",
			bundleSource: "https://www.tarantool.io/bundle.tar.gz",
			dstSetup: func(t *testing.T, baseDir string) string {
				t.Helper()

				conflictingFile := filepath.Join(baseDir, "conflict_dir")
				f, err := os.Create(conflictingFile)
				require.NoError(t, err)

				_ = f.Close()

				return baseDir
			},
			doer: &mockBundleDoer{
				token:   testToken,
				resBody: []byte("bundle content"),
			},
			errMsg: "failed to create destination file",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			dst := test.dstSetup(t, tmpDir)

			if test.doer != nil {
				test.doer.t = t
				test.doer.expectedURL = test.bundleSource
			}

			err := DownloadBundle(test.doer, test.bundleName, test.bundleSource, dst)

			if test.errMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), test.errMsg, "Error message mismatch")
				require.NoFileExists(t, filepath.Join(dst, test.bundleName),
					"a failed download must leave no file")
			} else {
				require.NoError(t, err, "DownloadBundle failed unexpectedly")

				require.FileExists(t, filepath.Join(dst, test.bundleName))

				destFilePath := filepath.Join(dst, test.bundleName)
				content, readErr := os.ReadFile(destFilePath)
				require.NoError(t, readErr, "Failed to read downloaded file")

				require.NotNil(t, test.doer, "tc.doer is nil in checkDownloadedFile block")
				require.Equal(t, test.doer.resBody, content, "Downloaded file content mismatch")
			}
		})
	}
}
