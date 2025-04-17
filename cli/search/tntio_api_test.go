package search_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
)

const (
	testingUsername = "test-user"
	testingPassword = "test-pass"
)

type platformInfo struct {
	arch string
	os   util.OsType
}

func (p *platformInfo) GetOs() (util.OsType, error) {
	return p.os, nil
}

func (p *platformInfo) GetArch() (string, error) {
	if p.arch == "" {
		return "", errors.New("mock architecture not applied")
	}
	return p.arch, nil
}

type mockDoer struct {
	t       *testing.T
	content map[string][]string
	query   string
}

// Do is a mock implementation of TntIoDoer interface method for unit testing purposes.
// It validates the HTTP request method, URL path, Content-Type header, and JSON body
// to ensure they match the expected values. If any of these validations fail, it logs an error
// and returns an appropriate error message. On success, it marshals the mock content
// into JSON and returns it as the response body.
func (m *mockDoer) Do(req *http.Request) ([]byte, error) {
	m.t.Helper()

	if req.Method != http.MethodPost {
		m.t.Errorf("expected POST method, got %s", req.Method)
		return nil, errors.New("invalid request method")
	}

	if req.URL.Path != "/en/accounts/customer_zone/api" {
		m.t.Errorf("expected /en/accounts/customer_zone/api path, got %s", req.URL.Path)
		return nil, errors.New("invalid request path")
	}

	if req.Header.Get("Content-Type") != "application/json" {
		m.t.Errorf("expected application/json content type, got %s",
			req.Header.Get("Content-Type"))
		return nil, errors.New("invalid content type")
	}

	// Read and check the request body.
	if req.Body == nil {
		m.t.Error("expected request body, got nil")
		return nil, errors.New("missing request body")
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		m.t.Errorf("failed to read request body: %v", err)
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	// Restore the body for potential re-reads
	req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	type expectedApiRequest struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Query    string `json:"query"`
	}

	var actualRequest expectedApiRequest
	err = json.Unmarshal(bodyBytes, &actualRequest)
	if err != nil {
		m.t.Errorf("failed to unmarshal request body: %v. Body: %s", err, string(bodyBytes))
		return nil, fmt.Errorf("failed to unmarshal request body: %w", err)
	}

	expectedRequest := expectedApiRequest{
		Username: testingUsername,
		Password: testingPassword,
		Query:    m.query,
	}

	require.Equal(m.t, expectedRequest, actualRequest, "request body mismatch")
	if m.t.Failed() {
		return nil, errors.New("invalid request body content")
	}

	return json.Marshal(m.content)
}

func (m *mockDoer) Token() string {
	return "mock-token"
}

func checkOutputVersionOrder(t *testing.T, got string, expected []string) {
	t.Helper()

	// Verify the output contains the expected versions in the correct order
	lastIndex := -1
	for _, ver := range expected {
		currentIndex := strings.Index(got, ver)
		require.True(t,
			currentIndex >= 0,
			"Expected version %q not found in output",
			ver,
		)
		if currentIndex >= 0 {
			require.True(t,
				currentIndex > lastIndex,
				"Version %q is not in the expected order",
				ver,
			)
			lastIndex = currentIndex
		}
	}

	// Ensure no unexpected versions are printed (optional, stricter check)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	require.Equal(t, len(expected), len(lines),
		"Output contains unexpected lines or missing expected versions")
}

func TestSearchVersions_TntIo(t *testing.T) {
	os.Setenv("TT_CLI_EE_USERNAME", testingUsername)
	os.Setenv("TT_CLI_EE_PASSWORD", testingPassword)
	defer os.Unsetenv("TT_CLI_EE_USERNAME")
	defer os.Unsetenv("TT_CLI_EE_PASSWORD")

	tests := map[string]struct {
		program          string
		platform         platformInfo
		specificVersion  string
		devBuilds        bool
		searchDebug      bool // Applied for tarantool EE search.
		doerContent      map[string][]string
		expectedQuery    string
		expectedVersions []string
		errMsg           string
	}{
		"tcm_release_all_versions": {
			program:  search.ProgramTcm,
			platform: platformInfo{arch: "x86_64", os: util.OsLinux},
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
			program:  search.ProgramEe,
			platform: platformInfo{arch: "x86_64", os: util.OsLinux},
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
			expectedVersions: []string{
				"2.8.4-0-r677",
				"2.8.4-0-r679",
				"2.8.4-0-r680",
				"gc64-3.2.0-0-r40",
				"gc64-3.3.1-0-r55",
				"gc64-3.3.2-0-r58",
				"gc64-3.3.2-0-r59",
			},
		},
		"tarantool_ee_debug_release_specific_versions": {
			program:         search.ProgramEe,
			platform:        platformInfo{arch: "x86_64", os: util.OsLinux},
			specificVersion: "3.2",
			searchDebug:     true,
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
		"tcm_dev_all_versions": {
			program:   search.ProgramTcm,
			platform:  platformInfo{arch: "x86_64", os: util.OsLinux},
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
			program:         search.ProgramTcm,
			platform:        platformInfo{arch: "x86_64", os: util.OsLinux},
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
			program:         search.ProgramTcm,
			platform:        platformInfo{arch: "aarch64", os: util.OsMacos},
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
			program:  search.ProgramTcm,
			platform: platformInfo{arch: "x86_64", os: util.OsUnknown},
			errMsg: "failed to fetch bundle info for " + search.ProgramTcm +
				": failed to get OS type for API: unsupported OS: " +
				strconv.Itoa(int(util.OsUnknown)),
		},
		"unsupported_arch": {
			program:  search.ProgramTcm,
			platform: platformInfo{arch: "arm", os: util.OsLinux},
			errMsg: "failed to fetch bundle info for tcm: " +
				"failed to get architecture: unsupported architecture: arm",
		},
		"empty_arch": {
			program:  search.ProgramTcm,
			platform: platformInfo{arch: "", os: util.OsLinux},
			errMsg:   "mock architecture not applied",
		},
		"api_error": {
			program:       search.ProgramTcm,
			platform:      platformInfo{arch: "x86_64", os: util.OsLinux},
			doerContent:   nil,
			expectedQuery: "tarantool-cluster-manager/release/linux/amd64",
			errMsg: "failed to fetch bundle info for tcm: " +
				"no packages found for this OS or release version",
		},
		"filter_out_all_sha256": {
			program:  search.ProgramTcm,
			platform: platformInfo{arch: "x86_64", os: util.OsLinux},
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
			errMsg: "failed to fetch bundle info for tcm: " +
				"no packages found for this OS or release version",
		},
	}

	opts := config.CliOpts{
		Env: &config.TtEnvOpts{
			BinDir: "/test/bin",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			originalStdout := os.Stdout
			var logBuf bytes.Buffer
			log.SetOutput(&logBuf)
			r, w, _ := os.Pipe()
			os.Stdout = w
			defer func() {
				os.Stdout = originalStdout
				log.SetOutput(os.Stderr)
			}()

			// Configure the mockDoer for this specific test case.
			mockDoer := mockDoer{
				t:       t,
				content: tt.doerContent,
				query:   tt.expectedQuery,
			}

			// Create SearchCtx with the configured mock.
			sCtx := search.NewSearchCtx(&tt.platform, &mockDoer)
			sCtx.ProgramName = tt.program
			sCtx.ReleaseVersion = tt.specificVersion
			sCtx.DevBuilds = tt.devBuilds
			if tt.searchDebug {
				sCtx.Filter = search.SearchDebug
			}

			err := search.SearchVersions(sCtx, &opts)

			w.Close()
			var outBuf bytes.Buffer
			_, readErr := outBuf.ReadFrom(r)
			require.NoError(t, readErr, "Failed to read from stdout pipe")
			gotOutput := outBuf.String()
			gotLog := logBuf.String()

			t.Logf("Log:\n%s", gotLog)

			if tt.errMsg != "" {
				require.Error(t, err, "Expected an error, but got nil")
				require.Contains(t, err.Error(), tt.errMsg,
					"Expected error message does not match")
			} else {
				require.NoError(t, err, "Expected no error, but got: %v", err)
				require.Contains(t,
					gotLog,
					"info Available versions of "+tt.program+":",
					"No info log found")

				t.Logf("Output:\n%s", gotOutput)
				checkOutputVersionOrder(t, gotOutput, tt.expectedVersions)
			}
		})
	}
}
