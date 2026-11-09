package customerzone

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errInvalidContentType        = errors.New("invalid content type")
	errInvalidRequestBodyContent = errors.New("invalid request body content")
	errInvalidRequestMethod      = errors.New("invalid request method")
	errInvalidRequestPath        = errors.New("invalid request path")
	errMissingRequestBody        = errors.New("missing request body")
	errUnknownDownload           = errors.New("unknown download")
)

const (
	testingUsername = "test-user"
	testingPassword = "test-pass"
	// mockSession is the session the mock API returns by default.
	mockSession = "mock-token"
	// packagesURL is where the customer zone keeps its archives.
	packagesURL = "https://www.tarantool.io/en/accounts/customer_zone/packages/"
)

// mockDoer is the customer zone at the HTTP level: the API answers a POST
// with content, the tt-ee catalog lists the keys of content and the archives
// of each of them, and downloads are served from files.
type mockDoer struct {
	t       *testing.T
	content map[string][]string
	// query is the query the API request must carry.
	query string
	// username and password are the credentials the API request must carry;
	// empty for the testing ones.
	username, password string
	// session is the token the API returns; empty for none.
	session string
	// token is the session of the client: empty until the API returns one.
	token string
	// files are the bodies of the downloads by URL.
	files map[string]string
	// calls counts the requests sent.
	calls int
	// downloads lists the URLs downloaded.
	downloads []string
}

func newMockDoer(t *testing.T, content map[string][]string, query string) *mockDoer {
	t.Helper()

	return &mockDoer{t: t, content: content, query: query, session: mockSession}
}

// Do is a mock implementation of TntIoDoer interface method for unit testing purposes.
// It validates the HTTP request method, URL path, Content-Type header, and JSON body
// to ensure they match the expected values. If any of these validations fail, it logs an error
// and returns an appropriate error message. On success, it marshals the mock content
// into JSON and returns it as the response body.
func (m *mockDoer) Do(req *http.Request) ([]byte, error) {
	m.t.Helper()

	m.calls++

	const catalogPath = "/en/accounts/customer_zone/packages/tt-ee"

	if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, catalogPath) {
		cookie, err := req.Cookie("sessionid")
		require.NoError(m.t, err)
		require.Equal(m.t, m.Token(), cookie.Value)

		var body strings.Builder

		if req.URL.Path == catalogPath {
			for selected := range m.content {
				fmt.Fprintf(&body, `<a href="%s/">%s</a>`, selected, selected)
			}
		} else {
			for _, archive := range m.content[path.Base(req.URL.Path)] {
				fmt.Fprintf(&body, `<a href="%s">%s</a>`, archive, archive)
			}
		}

		return []byte(body.String()), nil
	}

	if req.Method != http.MethodPost {
		m.t.Errorf("expected POST method, got %s", req.Method)

		return nil, errInvalidRequestMethod
	}

	if req.URL.Path != "/en/accounts/customer_zone/api" {
		m.t.Errorf("expected /en/accounts/customer_zone/api path, got %s", req.URL.Path)

		return nil, errInvalidRequestPath
	}

	if req.Header.Get("Content-Type") != "application/json" {
		m.t.Errorf("expected application/json content type, got %s",
			req.Header.Get("Content-Type"))

		return nil, errInvalidContentType
	}

	// Read and check the request body.
	if req.Body == nil {
		m.t.Error("expected request body, got nil")

		return nil, errMissingRequestBody
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		m.t.Errorf("failed to read request body: %v", err)

		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	type expectedAPIRequest struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Query    string `json:"query"`
	}

	var actualRequest expectedAPIRequest

	err = json.Unmarshal(bodyBytes, &actualRequest)
	if err != nil {
		m.t.Errorf("failed to unmarshal request body: %v. Body: %s", err, string(bodyBytes))

		return nil, fmt.Errorf("failed to unmarshal request body: %w", err)
	}

	expectedRequest := expectedAPIRequest{
		Username: testingUsername,
		Password: testingPassword,
		Query:    m.query,
	}
	if m.username != "" {
		expectedRequest.Username = m.username
		expectedRequest.Password = m.password
	}

	require.Equal(m.t, expectedRequest, actualRequest, "request body mismatch")
	require.NotEmpty(m.t, actualRequest.Query, "customer-zone API requires a nonempty query")

	if m.t.Failed() {
		return nil, errInvalidRequestBodyContent
	}

	m.token = m.session

	return json.Marshal(m.content)
}

// Download serves files with the session the API returned.
func (m *mockDoer) Download(req *http.Request, dst io.Writer) error {
	m.t.Helper()

	m.calls++

	require.Equal(m.t, http.MethodGet, req.Method)
	require.Equal(m.t, "tt", req.Header.Get("User-Agent"))

	cookie, err := req.Cookie("sessionid")
	require.NoError(m.t, err, "a download goes with the session")
	require.Equal(m.t, m.session, cookie.Value)

	body, ok := m.files[req.URL.String()]
	if !ok {
		m.t.Errorf("unexpected download %s", req.URL)

		return errUnknownDownload
	}

	m.downloads = append(m.downloads, req.URL.String())

	_, err = io.WriteString(dst, body)

	return err
}

func (m *mockDoer) Token() string {
	return m.token
}

// setTestingCredentials makes the testing credentials those of the environment.
func setTestingCredentials(t *testing.T) {
	t.Helper()

	t.Setenv("TT_CLI_EE_USERNAME", testingUsername)
	t.Setenv("TT_CLI_EE_PASSWORD", testingPassword)
}

// newQuery asks for the builds of program for the platform goos/goarch.
func newQuery(program binary.Program, goos, goarch string, dev bool, line string) bundle.Query {
	query := binary.NewBundleQuery(program, dev, line)

	query.OS = goos
	query.Arch = goarch

	return query
}

// buildFilter is which builds of a product a search shows.
type buildFilter int

const (
	// releaseBuilds leaves out the debug builds, as a search does by default.
	releaseBuilds buildFilter = iota
	// debugBuilds keeps only the debug builds.
	debugBuilds
	// allBuilds keeps every build.
	allBuilds
)

// listVersions lists the versions of the builds of program query asks source
// for, from the oldest to the newest, as tt binary search shows them.
func listVersions(t *testing.T, source bundle.Source, program binary.Program,
	query bundle.Query, filter buildFilter,
) ([]string, error) {
	t.Helper()

	bundles, err := binary.ListBundles(t.Context(), source, program, query)
	if err != nil {
		return nil, err
	}

	var versions []string

	for _, found := range bundles {
		if (filter == releaseBuilds && found.IsDebug()) ||
			(filter == debugBuilds && !found.IsDebug()) {
			continue
		}

		versions = append(versions, found.Version.Str)
	}

	return versions, nil
}

func TestSourceList(t *testing.T) {
	setTestingCredentials(t)

	pkgRelease := "tarantool-enterprise-sdk-nogc64-3.4.1-0-r63.linux.x86_64.tar.gz"
	pkgDebug := "tarantool-enterprise-sdk-debug-nogc64-3.4.1-0-r63.linux.x86_64.tar.gz"
	pkgOld := "tarantool-enterprise-sdk-gc64-2.11.1-0-r563.linux.x86_64.tar.gz"

	tests := map[string]struct {
		program          binary.Program
		goos, goarch     string
		specificVersion  string
		devBuilds        bool
		filter           buildFilter
		doerContent      map[string][]string
		expectedQuery    string
		expectedVersions []string
		errMsg           string
	}{
		"tt_ee_root_catalog": {
			program: binary.ProgramTtEe,
			goos:    "linux",
			goarch:  "amd64",
			doerContent: map[string][]string{
				"2.14.0": {"tt-ee-2.14.0-linux-amd64.tar.gz"},
				"2.15.0": {
					"tt-ee-2.15.0-linux-amd64.tar.gz",
					"tt-ee-2.15.0-linux-amd64.tar.gz.sha256",
					"tt-ee-2.15.0-linux-arm64.tar.gz",
					"tt-ee-2.15.0-darwin-amd64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.8.0-0-r94.linux.x86_64.tar.gz",
				},
			},
			expectedQuery:    "enterprise",
			expectedVersions: []string{"2.14.0", "2.15.0"},
		},
		"tt_ee_root_catalog_version_filter": {
			program:         binary.ProgramTtEe,
			goos:            "linux",
			goarch:          "amd64",
			specificVersion: "2.15",
			doerContent: map[string][]string{
				"2.14.0": {"tt-ee-2.14.0-linux-amd64.tar.gz"},
				"2.15.0": {
					"tt-ee-2.15.0-linux-amd64.tar.gz",
					"tt-ee-2.15.0-linux-arm64.tar.gz",
				},
				"2.15.1": {"tt-ee-2.15.1-linux-amd64.tar.gz"},
			},
			expectedQuery:    "enterprise",
			expectedVersions: []string{"2.15.0", "2.15.1"},
		},
		"tcm_release_all_versions": {
			program: binary.ProgramTcm,
			goos:    "linux",
			goarch:  "amd64",
			doerContent: map[string][]string{
				"1.3": {
					"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
					"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz.sha256",
					"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
					"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz.sha256",
				},
				"1.2": {
					"tcm-1.2.3-0-geae7e7d49.linux.amd64.tar.gz",
					"tcm-1.2.0-6-g0a82e719.linux.amd64.tar.gz",
					"tcm-1.2.1-0-gc2199e13e.linux.amd64.tar.gz",
					"tcm-1.2.0-11-g2d0a0f495.linux.amd64.tar.gz",
					"tcm-1.2.0-4-g59faf8b74.linux.amd64.tar.gz",
				},
				"1.1": {
					"tcm-1.1.0-21-g19693f57f.linux.amd64.tar.gz",
					"tcm-1.1.0-0-g5474dcc59.linux.amd64.tar.gz",
				},
			},
			expectedQuery: "tarantool-cluster-manager/release/linux/amd64",
			expectedVersions: []string{
				"1.1.0-0-g5474dcc59",
				"1.1.0-21-g19693f57f",
				"1.2.0-4-g59faf8b74",
				"1.2.0-6-g0a82e719",
				"1.2.0-11-g2d0a0f495",
				"1.2.1-0-gc2199e13e",
				"1.2.3-0-geae7e7d49",
				"1.3.0-0-g3857712a",
				"1.3.1-0-g074b5ffa",
			},
		},
		"tarantool_ee_release_all_versions": {
			program: binary.ProgramEe,
			goos:    "linux",
			goarch:  "amd64",
			doerContent: map[string][]string{
				"3.3": {
					"tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r59.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.3.2-0-r59.linux.x86_64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r59.linux.x86_64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r58.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.3.2-0-r58.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.3.2-0-r58.linux.x86_64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.3.2-0-r58.linux.x86_64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.3.1-0-r55.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.3.1-0-r55.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-debug-gc64-3.3.1-0-r55.linux.x86_64.tar.gz.sha256",
					"tarantool-enterprise-sdk-gc64-3.3.1-0-r55.linux.x86_64.tar.gz.sha256",
				},
				"2.8": {
					"tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz",
					"tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz",
					"tarantool-enterprise-sdk-2.8.4-0-r680.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-2.8.4-0-r680.tar.gz.sha256",
					"tarantool-enterprise-sdk-2.8.4-0-r679.tar.gz",
					"tarantool-enterprise-sdk-debug-2.8.4-0-r679.tar.gz",
					"tarantool-enterprise-sdk-debug-2.8.4-0-r679.tar.gz.sha256",
					"tarantool-enterprise-sdk-2.8.4-0-r679.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-2.8.4-0-r677.tar.gz",
					"tarantool-enterprise-sdk-2.8.4-0-r677.tar.gz",
					"tarantool-enterprise-sdk-2.8.4-0-r677.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-2.8.4-0-r677.tar.gz.sha256",
				},
				"3.2": {
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.linux.x86_64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.linux.x86_64.tar.gz.sha256",
				},
			},
			expectedQuery: "enterprise/release/linux/x86_64",
			// Tarantool 2.x archives are not offered.
			expectedVersions: []string{
				"gc64-3.2.0-0-r40",
				"gc64-3.3.1-0-r55",
				"gc64-3.3.2-0-r58",
				"gc64-3.3.2-0-r59",
			},
		},
		"tarantool_ee_debug_release_specific_versions": {
			program:         binary.ProgramEe,
			goos:            "linux",
			goarch:          "amd64",
			specificVersion: "3.2",
			filter:          debugBuilds,
			doerContent: map[string][]string{
				"3.2": {
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.linux.x86_64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.linux.x86_64.tar.gz.sha256",
				},
			},
			expectedQuery: "enterprise/release/linux/x86_64/3.2",
			expectedVersions: []string{
				"debug-gc64-3.2.0-0-r40",
			},
		},
		"tarantool_ee_all_builds": {
			program:         binary.ProgramEe,
			goos:            "linux",
			goarch:          "amd64",
			specificVersion: "3.2",
			filter:          allBuilds,
			doerContent: map[string][]string{
				"3.2": {
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.linux.x86_64.tar.gz",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.linux.x86_64.tar.gz",
				},
			},
			expectedQuery: "enterprise/release/linux/x86_64/3.2",
			expectedVersions: []string{
				"gc64-3.2.0-0-r40",
				"debug-gc64-3.2.0-0-r40",
			},
		},
		"tcm_dev_all_versions": {
			program:   binary.ProgramTcm,
			goos:      "linux",
			goarch:    "amd64",
			devBuilds: true,
			doerContent: map[string][]string{
				"0.1": {
					"tcm-0.1.0-beta1-6-gdcfecb64.linux.amd64.tar.gz",
					"tcm-0.1.0-beta1-2-g0a509f42.linux.amd64.tar.gz",
					"tcm-0.1.0-alpha3-16-g1a2f9e96.linux.amd64.tar.gz",
					"tcm-0.1.0-alpha3-0-gd97fca66.linux.amd64.tar.gz",
					"tcm-0.1.0-alpha2-0-g9b411115.linux.amd64.tar.gz",
					"tcm-0.1.0-alpha1-0-g459a917c.linux.amd64.tar.gz",
				},
				"1.2": {
					"tcm-1.2.3-24-gab93333e.linux.amd64.tar.gz",
					"tcm-1.2.3-23-g2c1e1c11.linux.amd64.tar.gz",
					"tcm-1.2.3-21-g1b77d657.linux.amd64.tar.gz",
					"tcm-1.2.3-20-gbcabd286d.linux.amd64.tar.gz",
					"tcm-1.2.0-11-g2d0a0f495.linux.amd64.tar.gz",
					"tcm-1.2.0-7-g438395e3f.linux.amd64.tar.gz",
					"tcm-1.2.0-6-g0a82e719.linux.amd64.tar.gz",
					"tcm-1.2.0-5-g82c29b37.linux.amd64.tar.gz",
				},
			},
			expectedQuery: "tarantool-cluster-manager/dev/linux/amd64",
			expectedVersions: []string{
				"0.1.0-alpha1-0-g459a917c",
				"0.1.0-alpha2-0-g9b411115",
				"0.1.0-alpha3-0-gd97fca66",
				"0.1.0-alpha3-16-g1a2f9e96",
				"0.1.0-beta1-2-g0a509f42",
				"0.1.0-beta1-6-gdcfecb64",
				"1.2.0-5-g82c29b37",
				"1.2.0-6-g0a82e719",
				"1.2.0-7-g438395e3f",
				"1.2.0-11-g2d0a0f495",
				"1.2.3-20-gbcabd286d",
				"1.2.3-21-g1b77d657",
				"1.2.3-23-g2c1e1c11",
				"1.2.3-24-gab93333e",
			},
		},
		"tcm_release_specific_version": {
			program:         binary.ProgramTcm,
			goos:            "linux",
			goarch:          "amd64",
			specificVersion: "1.2",
			doerContent: map[string][]string{
				"1.2": {
					"tcm-1.2.3-0-geae7e7d49.linux.amd64.tar.gz",
					"tcm-1.2.1-0-gc2199e13e.linux.amd64.tar.gz",
				},
			},
			expectedQuery: "tarantool-cluster-manager/release/linux/amd64/1.2",
			expectedVersions: []string{
				"1.2.1-0-gc2199e13e",
				"1.2.3-0-geae7e7d49",
			},
		},
		"tcm_dev_specific_macos_version": {
			program:         binary.ProgramTcm,
			goos:            "darwin",
			goarch:          "arm64",
			specificVersion: "0.1",
			devBuilds:       true,
			doerContent: map[string][]string{
				"0.1": {
					"tcm-0.1.0-alpha3-0-gd97fca66.macos.arm64.tar.gz",
					"tcm-0.1.0-alpha2-0-g9b411115.macos.arm64.tar.gz",
					"tcm-0.1.0-alpha1-0-g459a917c.macos.arm64.tar.gz",
				},
			},
			expectedQuery: "tarantool-cluster-manager/dev/macos/arm64/0.1",
			expectedVersions: []string{
				"0.1.0-alpha1-0-g459a917c",
				"0.1.0-alpha2-0-g9b411115",
				"0.1.0-alpha3-0-gd97fca66",
			},
		},
		"unknown_os": {
			program: binary.ProgramTcm,
			goos:    "windows",
			goarch:  "amd64",
			errMsg:  "failed to get OS type for API: unsupported OS: windows",
		},
		"unsupported_arch": {
			program: binary.ProgramTcm,
			goos:    "linux",
			goarch:  "arm",
			errMsg:  "failed to get architecture: unsupported architecture: arm",
		},
		"empty_arch": {
			program: binary.ProgramTcm,
			goos:    "linux",
			goarch:  "",
			errMsg:  "failed to get architecture: unsupported architecture: ",
		},
		// An empty reply is an empty result: the search reports it.
		"api_no_packages": {
			program:       binary.ProgramTcm,
			goos:          "linux",
			goarch:        "amd64",
			doerContent:   nil,
			expectedQuery: "tarantool-cluster-manager/release/linux/amd64",
		},
		"filter_out_all_sha256": {
			program: binary.ProgramTcm,
			goos:    "linux",
			goarch:  "amd64",
			doerContent: map[string][]string{
				"1.3": {
					"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz.sha256",
					"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz.sha256",
					"tcm-1.2.3-0-geae7e7d49.linux.amd64.tar.gz.sha256",
					"tcm-1.2.0-6-g0a82e719.linux.amd64.tar.gz.sha256",
					"tcm-1.2.0-4-g59faf8b74.linux.amd64.tar.gz.sha256",
					"tcm-1.2.0-11-g2d0a0f495.linux.amd64.tar.gz.sha256",
					"tcm-1.2.0-7-g438395e3f.linux.amd64.tar.gz.sha256",
				},
			},
			expectedQuery: "tarantool-cluster-manager/release/linux/amd64",
		},
		"random_data": {
			program:       binary.ProgramEe,
			goos:          "linux",
			goarch:        "amd64",
			doerContent:   map[string][]string{"random": {"data"}},
			expectedQuery: "enterprise/release/linux/x86_64",
		},
		"search_release": {
			program:          binary.ProgramEe,
			goos:             "linux",
			goarch:           "amd64",
			doerContent:      map[string][]string{"3.4": {pkgRelease, pkgDebug}},
			expectedQuery:    "enterprise/release/linux/x86_64",
			expectedVersions: []string{"nogc64-3.4.1-0-r63"},
		},
		"search_debug": {
			program:          binary.ProgramEe,
			goos:             "linux",
			goarch:           "amd64",
			filter:           debugBuilds,
			doerContent:      map[string][]string{"3.4": {pkgRelease, pkgDebug}},
			expectedQuery:    "enterprise/release/linux/x86_64",
			expectedVersions: []string{"debug-nogc64-3.4.1-0-r63"},
		},
		"tarantool_2_is_not_offered": {
			program:          binary.ProgramEe,
			goos:             "linux",
			goarch:           "amd64",
			doerContent:      map[string][]string{"2.11": {pkgOld}, "3.4": {pkgRelease}},
			expectedQuery:    "enterprise/release/linux/x86_64",
			expectedVersions: []string{"nogc64-3.4.1-0-r63"},
		},
		"only_tarantool_2": {
			program:       binary.ProgramEe,
			goos:          "linux",
			goarch:        "amd64",
			doerContent:   map[string][]string{"2.11": {pkgOld}},
			expectedQuery: "enterprise/release/linux/x86_64",
		},
		"search_release_of_debug_only": {
			program:       binary.ProgramEe,
			goos:          "linux",
			goarch:        "amd64",
			doerContent:   map[string][]string{"3.4": {pkgDebug}},
			expectedQuery: "enterprise/release/linux/x86_64",
		},
		"search_debug_of_release_only": {
			program:       binary.ProgramEe,
			goos:          "linux",
			goarch:        "amd64",
			filter:        debugBuilds,
			doerContent:   map[string][]string{"3.4": {pkgRelease}},
			expectedQuery: "enterprise/release/linux/x86_64",
		},
		// The version of an archive tt cannot read is left out.
		"invalid_version": {
			program: binary.ProgramEe,
			goos:    "linux",
			goarch:  "amd64",
			doerContent: map[string][]string{
				"3.1": {"tarantool-enterprise-sdk-gc64-Ver3.1.linux.x86_64.tar.gz"},
			},
			expectedQuery: "enterprise/release/linux/x86_64",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			doer := newMockDoer(t, tt.doerContent, tt.expectedQuery)
			query := newQuery(tt.program, tt.goos, tt.goarch, tt.devBuilds, tt.specificVersion)

			versions, err := listVersions(t, newSource(doer), tt.program, query, tt.filter)
			if tt.errMsg != "" {
				require.ErrorContains(t, err, tt.errMsg)
				require.Zero(t, doer.calls, "the platform is checked before any request")

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expectedVersions, versions)
		})
	}
}

func TestSourceListTtEeWithoutSession(t *testing.T) {
	setTestingCredentials(t)

	doer := newMockDoer(t, map[string][]string{
		"2.15.0": {"tt-ee-2.15.0-linux-amd64.tar.gz"},
	}, "enterprise")

	doer.session = ""

	_, err := newSource(doer).List(t.Context(),
		newQuery(binary.ProgramTtEe, "linux", "amd64", false, ""))
	require.ErrorIs(t, err, errMissingCatalogSession)
}

func TestSourceListLocators(t *testing.T) {
	setTestingCredentials(t)

	tests := map[string]struct {
		program      binary.Program
		goos, goarch string
		devBuilds    bool
		release      string
		tarball      string
		query        string
		expected     string
	}{
		"tt-ee x86 linux": {
			program:  binary.ProgramTtEe,
			goos:     "linux",
			goarch:   "amd64",
			release:  "2.15.0",
			tarball:  "tt-ee-2.15.0-linux-amd64.tar.gz",
			query:    "enterprise",
			expected: packagesURL + "tt-ee/2.15.0/tt-ee-2.15.0-linux-amd64.tar.gz",
		},
		"tcm x86 linux": {
			program: binary.ProgramTcm,
			goos:    "linux",
			goarch:  "amd64",
			release: "1.3",
			tarball: "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
			query:   "tarantool-cluster-manager/release/linux/amd64",
			expected: packagesURL + "tarantool-cluster-manager/release/linux/amd64/1.3/" +
				"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
		},
		"tcm arm macos": {
			program:   binary.ProgramTcm,
			goos:      "darwin",
			goarch:    "arm64",
			devBuilds: true,
			release:   "1.1",
			tarball:   "tcm-1.1.0-0-g5474dcc59.macos.arm64.tar.gz",
			query:     "tarantool-cluster-manager/dev/macos/arm64",
			expected: packagesURL + "tarantool-cluster-manager/dev/macos/arm64/1.1/" +
				"tcm-1.1.0-0-g5474dcc59.macos.arm64.tar.gz",
		},
		"tarantool x86 linux": {
			program: binary.ProgramEe,
			goos:    "linux",
			goarch:  "amd64",
			release: "3.0",
			tarball: "tarantool-enterprise-sdk-gc64-3.0.0-0-r1.linux.x86_64.tar.gz",
			query:   "enterprise/release/linux/x86_64",
			expected: packagesURL + "enterprise/release/linux/x86_64/3.0/" +
				"tarantool-enterprise-sdk-gc64-3.0.0-0-r1.linux.x86_64.tar.gz",
		},
		"tarantool arm macos": {
			program:   binary.ProgramEe,
			goos:      "darwin",
			goarch:    "arm64",
			devBuilds: true,
			release:   "3.3",
			tarball:   "tarantool-enterprise-sdk-gc64-3.3.0-0-r1.macos.aarch64.tar.gz",
			query:     "enterprise/dev/macos/aarch64",
			expected: packagesURL + "enterprise/dev/macos/aarch64/3.3/" +
				"tarantool-enterprise-sdk-gc64-3.3.0-0-r1.macos.aarch64.tar.gz",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			doer := newMockDoer(t, map[string][]string{tt.release: {tt.tarball}}, tt.query)
			query := newQuery(tt.program, tt.goos, tt.goarch, tt.devBuilds, "")

			artifacts, err := newSource(doer).List(t.Context(), query)
			require.NoError(t, err)
			require.Len(t, artifacts, 1)
			require.Equal(t, tt.tarball, artifacts[0].Name)
			require.Equal(t, tt.expected, artifacts[0].Locator)
		})
	}
}

func TestPackageQuery(t *testing.T) {
	tests := map[string]struct {
		program      binary.Program
		goos, goarch string
		devBuilds    bool
		release      string
		expected     string
		errMsg       string
	}{
		"tt-ee has no platform directories": {
			program:  binary.ProgramTtEe,
			goos:     "darwin",
			goarch:   "arm64",
			release:  "2.15.0",
			expected: "tt-ee/2.15.0",
		},
		"tcm x86 linux": {
			program:  binary.ProgramTcm,
			goos:     "linux",
			goarch:   "amd64",
			release:  "1.3",
			expected: "tarantool-cluster-manager/release/linux/amd64/1.3",
		},
		"tcm arm macos": {
			program:   binary.ProgramTcm,
			goos:      "darwin",
			goarch:    "arm64",
			release:   "1.1",
			devBuilds: true,
			expected:  "tarantool-cluster-manager/dev/macos/arm64/1.1",
		},
		"tarantool x86 linux": {
			program:  binary.ProgramEe,
			goos:     "linux",
			goarch:   "amd64",
			release:  "3.0",
			expected: "enterprise/release/linux/x86_64/3.0",
		},
		"tarantool arm macos": {
			program:   binary.ProgramEe,
			goos:      "darwin",
			goarch:    "arm64",
			release:   "3.3",
			devBuilds: true,
			expected:  "enterprise/dev/macos/aarch64/3.3",
		},
		"sdk whole package": {
			program:  binary.ProgramSDK,
			goos:     "linux",
			goarch:   "arm64",
			expected: "enterprise/release/linux/aarch64",
		},
		"no platform": {
			program: binary.ProgramEe,
			release: "3.0",
			errMsg:  "failed to get architecture: unsupported architecture: ",
		},
		"wrong arch": {
			program: binary.ProgramEe,
			goos:    "linux",
			goarch:  "arm",
			release: "3.0",
			errMsg:  "failed to get architecture: unsupported architecture: arm",
		},
		"uname arch": {
			program: binary.ProgramEe,
			goos:    "linux",
			goarch:  "x86_64",
			release: "3.0",
			errMsg:  "unsupported architecture: x86_64",
		},
		"empty arch": {
			program: binary.ProgramEe,
			goos:    "linux",
			goarch:  "",
			release: "3.0",
			errMsg:  "failed to get architecture: unsupported architecture: ",
		},
		"wrong os": {
			program: binary.ProgramEe,
			goos:    "windows",
			goarch:  "amd64",
			release: "3.0",
			errMsg:  "failed to get OS type for API: unsupported OS: windows",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			query := newQuery(tt.program, tt.goos, tt.goarch, tt.devBuilds, "")

			got, err := packageQuery(tt.program, query, tt.release)
			if tt.errMsg != "" {
				require.ErrorContains(t, err, tt.errMsg)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestTtEeRejectsPlatformBeforeCredentials(t *testing.T) {
	for _, platform := range []struct{ goos, goarch string }{
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "windows", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "linux", goarch: "arm"},
	} {
		doer := newMockDoer(t, nil, "")
		source := newSource(doer)

		source.credentialsFile = "missing-credentials"

		_, err := source.List(t.Context(),
			newQuery(binary.ProgramTtEe, platform.goos, platform.goarch, false, ""))
		require.ErrorContains(t, err, "unsupported", platform)
		require.NotContains(t, err.Error(), "credentials", platform)
		require.Zero(t, doer.calls, platform)
	}
}

func TestSourceListUnknownProduct(t *testing.T) {
	for _, program := range []binary.Program{
		binary.ProgramCe, binary.ProgramTt, binary.ProgramUnknown,
	} {
		doer := newMockDoer(t, nil, "")

		_, err := newSource(doer).List(t.Context(), newQuery(program, "linux", "amd64", false, ""))
		require.ErrorContains(t, err, "not a product of the customer zone: ", program.String())
		require.Zero(t, doer.calls, program.String())
	}
}

func TestSourceCredentialsFile(t *testing.T) {
	tests := map[string]struct {
		program         binary.Program
		devBuilds       bool
		release         string
		tarball         string
		query           string
		expectedVersion string
		expectedURI     string
	}{
		"tarantool-ee": {
			program:         binary.ProgramEe,
			release:         "3.4",
			tarball:         "tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz",
			query:           "enterprise/release/linux/x86_64",
			expectedVersion: "gc64-3.4.0-0-r60",
			expectedURI: PkgURI + "/enterprise/release/linux/x86_64/3.4/" +
				"tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz",
		},
		"tarantool-sdk dev": {
			program:         binary.ProgramSDK,
			devBuilds:       true,
			release:         "3.4",
			tarball:         "tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz",
			query:           "enterprise/dev/linux/x86_64",
			expectedVersion: "gc64-3.4.0-0-r60",
			expectedURI: PkgURI + "/enterprise/dev/linux/x86_64/3.4/" +
				"tarantool-enterprise-sdk-gc64-3.4.0-0-r60.linux.x86_64.tar.gz",
		},
		"tcm": {
			program:         binary.ProgramTcm,
			release:         "1.3",
			tarball:         "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
			query:           "tarantool-cluster-manager/release/linux/amd64",
			expectedVersion: "1.3.1-0-g074b5ffa",
			expectedURI: PkgURI + "/tarantool-cluster-manager/release/linux/amd64/1.3/" +
				"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TT_CLI_EE_USERNAME", "ignored-user")
			t.Setenv("TT_CLI_EE_PASSWORD", "ignored-password")

			credentials := filepath.Join(t.TempDir(), "credentials")
			require.NoError(t, os.WriteFile(credentials,
				[]byte("file-user\nfile-password\n"), 0o600))

			doer := newMockDoer(t, map[string][]string{tt.release: {tt.tarball}}, tt.query)

			doer.username = "file-user"
			doer.password = "file-password"

			source := newSource(doer)
			flags := pflag.NewFlagSet("search", pflag.ContinueOnError)
			source.RegisterFlags(tt.program.String(), flags)
			require.NoError(t, flags.Parse([]string{"--credentials-file", credentials}))

			bundles, err := binary.ListBundles(t.Context(), source, tt.program,
				newQuery(tt.program, "linux", "amd64", tt.devBuilds, ""))
			require.NoError(t, err)
			require.Len(t, bundles, 1)
			require.Equal(t, tt.expectedVersion, bundles[0].Version.Str)
			require.Equal(t, tt.tarball, bundles[0].Version.Tarball)
			require.Equal(t, tt.expectedURI, bundles[0].Artifact.Locator)
		})
	}
}

func TestSourceListBundles(t *testing.T) {
	setTestingCredentials(t)

	type listed struct {
		version version.Version
		locator string
	}

	tests := map[string]struct {
		program         binary.Program
		goos, goarch    string
		doerContent     map[string][]string
		specificVersion string
		filter          buildFilter
		expectedQuery   string
		expected        []listed
	}{
		// spell-checker:disable
		"tcm_release_bundles": {
			program: binary.ProgramTcm,
			goos:    "linux",
			goarch:  "amd64",
			doerContent: map[string][]string{
				"1.3": {
					"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
					"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
				},
				"1.2": {
					"tcm-1.2.3-0-geae7e7d49.linux.amd64.tar.gz",
					"tcm-1.2.1-0-gc2199e13e.linux.amd64.tar.gz",
				},
			},
			expectedQuery: "tarantool-cluster-manager/release/linux/amd64",
			expected: []listed{
				{
					version: version.Version{
						Major:   1,
						Minor:   2,
						Patch:   1,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "gc2199e13e",
						Tarball: "tcm-1.2.1-0-gc2199e13e.linux.amd64.tar.gz",
						Str:     "1.2.1-0-gc2199e13e",
					},
					locator: PkgURI + "/tarantool-cluster-manager/release/linux/amd64/1.2/" +
						"tcm-1.2.1-0-gc2199e13e.linux.amd64.tar.gz",
				},
				{
					version: version.Version{
						Major:   1,
						Minor:   2,
						Patch:   3,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "geae7e7d49",
						Tarball: "tcm-1.2.3-0-geae7e7d49.linux.amd64.tar.gz",
						Str:     "1.2.3-0-geae7e7d49",
					},
					locator: PkgURI + "/tarantool-cluster-manager/release/linux/amd64/1.2/" +
						"tcm-1.2.3-0-geae7e7d49.linux.amd64.tar.gz",
				},
				{
					version: version.Version{
						Major:   1,
						Minor:   3,
						Patch:   0,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "g3857712a",
						Tarball: "tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
						Str:     "1.3.0-0-g3857712a",
					},
					locator: PkgURI + "/tarantool-cluster-manager/release/linux/amd64/1.3/" +
						"tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
				},
				{
					version: version.Version{
						Major:   1,
						Minor:   3,
						Patch:   1,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "g074b5ffa",
						Tarball: "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
						Str:     "1.3.1-0-g074b5ffa",
					},
					locator: PkgURI + "/tarantool-cluster-manager/release/linux/amd64/1.3/" +
						"tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
				},
			},
		},

		"tarantool_ee_debug_release_specific_versions": {
			program:         binary.ProgramEe,
			goos:            "darwin",
			goarch:          "arm64",
			specificVersion: "3.2",
			filter:          debugBuilds,
			doerContent: map[string][]string{
				"3.2": {
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.macos.aarch64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.macos.aarch64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.macos.aarch64.tar.gz",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.macos.aarch64.tar.gz.sha256",
				},
			},
			expectedQuery: "enterprise/release/macos/aarch64/3.2",
			expected: []listed{
				{
					version: version.Version{
						Major:   3,
						Minor:   2,
						Patch:   0,
						Release: version.Release{Type: version.TypeRelease},
						Tarball: "tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40" +
							".macos.aarch64.tar.gz",
						Str:       "debug-gc64-3.2.0-0-r40",
						BuildName: "debug-gc64",
						Revision:  40,
					},
					locator: PkgURI + "/enterprise/release/macos/aarch64/3.2/" +
						"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.macos.aarch64.tar.gz",
				},
			},
		},
		// spell-checker:enable
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			doer := newMockDoer(t, tt.doerContent, tt.expectedQuery)
			query := newQuery(tt.program, tt.goos, tt.goarch, false, tt.specificVersion)

			bundles, err := binary.ListBundles(t.Context(), newSource(doer), tt.program, query)
			require.NoError(t, err)

			var got []listed

			for _, found := range bundles {
				if tt.filter == debugBuilds && !found.IsDebug() {
					continue
				}

				got = append(got, listed{version: found.Version, locator: found.Artifact.Locator})
			}

			require.Equal(t, tt.expected, got)
		})
	}
}

func TestSourceFetch(t *testing.T) {
	setTestingCredentials(t)

	const (
		tarball = "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz"
		session = "session-from-api"
		body    = "the bytes of the archive"
	)

	locator := PkgURI + "/tarantool-cluster-manager/release/linux/amd64/1.3/" + tarball
	doer := newMockDoer(t, map[string][]string{"1.3": {tarball}},
		"tarantool-cluster-manager/release/linux/amd64")

	doer.session = session
	doer.files = map[string]string{locator: body}

	source := newSource(doer)

	artifacts, err := source.List(t.Context(),
		newQuery(binary.ProgramTcm, "linux", "amd64", false, ""))
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, locator, artifacts[0].Locator)

	var dst bytes.Buffer

	require.NoError(t, source.Fetch(t.Context(), artifacts[0], &dst))
	require.Equal(t, body, dst.String())
	require.Equal(t, []string{locator}, doer.downloads)
}

func TestAPIPackage(t *testing.T) {
	tests := map[string]struct {
		input    binary.Program
		expected string
	}{
		"tarantool enterprise edition": {
			input:    binary.ProgramEe,
			expected: "enterprise",
		},

		"tarantool sdk": {
			input:    binary.ProgramSDK,
			expected: "enterprise",
		},
		"tt enterprise edition": {
			input:    binary.ProgramTtEe,
			expected: "tt-ee",
		},

		"tcm": {
			input:    binary.ProgramTcm,
			expected: "tarantool-cluster-manager",
		},

		"tarantool community edition": {
			input:    binary.ProgramCe,
			expected: "",
		},

		"tt cli": {
			input:    binary.ProgramTt,
			expected: "",
		},

		"unknown program": {
			input:    binary.ProgramUnknown,
			expected: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.expected, apiPackage(tc.input))
		})
	}
}
