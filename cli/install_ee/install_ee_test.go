package install_ee

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/search"
)

const (
	// testToken is a token for the TntIoDownloader.
	testToken = "test-token"
)

// mockBundleDoer is a mock implementation of search.TntIoDoer for DownloadBundle tests.
type mockBundleDoer struct {
	t           *testing.T
	token       string
	resBody     []byte
	resErr      error
	expectedUrl string
}

// Do mocks the Do method of search.TntIoDoer.
// It verifies the request's URL, method, User-Agent header, and sessionid cookie.
func (m *mockBundleDoer) Do(req *http.Request) ([]byte, error) {
	m.t.Helper()

	require.Equal(m.t, m.expectedUrl, req.URL.String(), "Request URL mismatch")
	require.Equal(m.t, http.MethodGet, req.Method, "Request method mismatch")
	require.Equal(m.t, "tt", req.Header.Get("User-Agent"), "User-Agent header mismatch")

	if m.token != "" {
		cookie, err := req.Cookie("sessionid")
		require.NoError(m.t, err, "sessionid cookie not found when token is non-empty")
		require.NotNil(m.t, cookie, "sessionid cookie is nil when token is non-empty")
		require.Equal(m.t, m.token, cookie.Value, "sessionid cookie value mismatch")
	} else {
		_, err := req.Cookie("sessionid")
		require.ErrorIs(m.t, err, http.ErrNoCookie,
			"sessionid cookie should not be present when token is empty")
	}

	if m.resErr != nil {
		return nil, m.resErr
	}
	return m.resBody, nil
}

// Token mocks the Token method of search.TntIoDoer.
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
			bundleSource: "http://tarantool.io/bundle_with_token.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:   testToken,
				resBody: []byte("bundle content with token"),
			},
		},

		"successful download no token": {
			bundleName:   "bundle_no_token.tar.gz",
			bundleSource: "http://tarantool.io/bundle_no_token.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:   "",
				resBody: []byte("bundle content no token"),
			},
		},

		"error from TntIoDoer": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "http://tarantool.io/bundle.tar.gz",
			dstSetup:     defaultDstSetup,
			doer: &mockBundleDoer{
				token:  testToken,
				resErr: errors.New("simulated network error"),
			},
			errMsg: "simulated network error",
		},

		"nil TntIoDoer in searchCtx": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "http://tarantool.io/bundle.tar.gz",
			dstSetup:     defaultDstSetup,
			doer:         nil,
			errMsg:       "no tarantool.io doer was applied",
		},

		"destination directory does not exist": {
			bundleName:   "bundle.tar.gz",
			bundleSource: "http://tarantool.io/bundle.tar.gz",
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
			bundleSource: "http://tarantool.io/bundle.tar.gz",
			dstSetup: func(t *testing.T, baseDir string) string {
				t.Helper()
				file, err := os.CreateTemp(baseDir, "destination_as_file_*")
				require.NoError(t, err)
				filePath := file.Name()
				file.Close()
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
			bundleSource: "http://tarantool.io/bundle.tar.gz",
			dstSetup: func(t *testing.T, baseDir string) string {
				t.Helper()
				conflictingFile := filepath.Join(baseDir, "conflict_dir")
				f, err := os.Create(conflictingFile)
				require.NoError(t, err)
				f.Close()
				return baseDir
			},
			doer: &mockBundleDoer{
				token:   testToken,
				resBody: []byte("bundle content"),
			},
			errMsg: "failed to create destination file",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			dst := tc.dstSetup(t, tmpDir)

			if tc.doer != nil {
				tc.doer.t = t
				tc.doer.expectedUrl = tc.bundleSource
			}
			err := DownloadBundle(tc.doer, tc.bundleName, tc.bundleSource, dst)

			if tc.errMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errMsg, "Error message mismatch")
			} else {
				require.NoError(t, err, "DownloadBundle failed unexpectedly")

				require.FileExists(t, filepath.Join(dst, tc.bundleName))
				destFilePath := filepath.Join(dst, tc.bundleName)
				content, readErr := os.ReadFile(destFilePath)
				require.NoError(t, readErr, "Failed to read downloaded file")

				require.NotNil(t, tc.doer, "tc.doer is nil in checkDownloadedFile block")
				require.Equal(t, tc.doer.resBody, content, "Downloaded file content mismatch")
			}
		})
	}
}

func TestNewTntIoDownloader(t *testing.T) {
	tests := map[string]struct {
		token       string
		expectToken string
		requestHost string
	}{
		"empty token": {
			token:       "",
			expectToken: "",
			requestHost: "tarantool.io",
		},

		"non-empty token": {
			token:       testToken,
			expectToken: testToken,
			requestHost: "tarantool.io",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			downloader := NewTntIoDownloader(tc.token)
			require.NotNil(t, downloader, "NewTntIoDownloader should return a non-nil object")
			require.Implements(t, (*search.TntIoDoer)(nil), downloader,
				"NewTntIoDownloader should return an object implementing search.TntIoDoer")

			require.Equal(t, tc.expectToken, downloader.token, "Internal token field mismatch")
			require.Equal(t, tc.expectToken, downloader.Token(),
				"interface Token() method should return the correct token")

			client := downloader.client
			require.NotNil(t, client, "downloader.client should not be nil")

			require.NotNil(t, client.CheckRedirect, "CheckRedirect function is nil")

			// Using a dummy request to test the CheckRedirect behavior.
			requestURL := fmt.Sprintf("http://%s/testpath", tc.requestHost)
			dummyReq, err := http.NewRequest(http.MethodGet, requestURL, nil)
			require.NoError(t, err, "Failed to create dummy HTTP request")

			// Call the CheckRedirect to add cookies to dummyReq and set Host.
			err = client.CheckRedirect(dummyReq, nil)
			require.NoError(t, err, "CheckRedirect function returned an error")

			// Verify the Host field was set correctly.
			require.Equal(t, tc.requestHost, dummyReq.Host,
				"request Host mismatch after CheckRedirect")

			// Verify the sessionid cookie based on the token.
			if tc.token != "" {
				cookie, err := dummyReq.Cookie("sessionid")
				require.NoError(t, err, "sessionid cookie not found when token is non-empty")
				require.NotNil(t, cookie, "sessionid cookie is nil when token is non-empty")
				require.Equal(t, tc.expectToken, cookie.Value, "sessionid cookie value mismatch")
			} else {
				_, err := dummyReq.Cookie("sessionid")
				require.ErrorIs(t, err, http.ErrNoCookie,
					"sessionid cookie should not be present when token is empty")
			}
		})
	}
}
