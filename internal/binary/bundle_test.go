package binary_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestFetchBundlesInfo(t *testing.T) {
	t.Setenv("TT_CLI_EE_USERNAME", testingUsername)
	t.Setenv("TT_CLI_EE_PASSWORD", testingPassword)

	defer func() {
		_ = os.Unsetenv("TT_CLI_EE_USERNAME")
	}()
	defer func() {
		_ = os.Unsetenv("TT_CLI_EE_PASSWORD")
	}()

	tests := map[string]struct {
		program         binary.Program
		platform        platformInfo
		doerContent     map[string][]string
		specificVersion string
		searchDebug     bool // Applied for tarantool EE binary.
		expectedQuery   string
		expectedBundles binary.BundleInfoSlice
		errMsg          string
	}{
		// spell-checker:disable
		"tcm_release_bundles": {
			program:  binary.ProgramTcm,
			platform: platformInfo{arch: "x86_64", os: binary.OsLinux},
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
			expectedBundles: binary.BundleInfoSlice{
				{
					Package: "tarantool-cluster-manager",
					Release: "1.2",
					Token:   "mock-token",
					Version: version.Version{
						Major:   1,
						Minor:   2,
						Patch:   1,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "gc2199e13e",
						Tarball: "tcm-1.2.1-0-gc2199e13e.linux.amd64.tar.gz",
						Str:     "1.2.1-0-gc2199e13e",
					},
				},
				{
					Package: "tarantool-cluster-manager",
					Release: "1.2",
					Token:   "mock-token",
					Version: version.Version{
						Major:   1,
						Minor:   2,
						Patch:   3,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "geae7e7d49",
						Tarball: "tcm-1.2.3-0-geae7e7d49.linux.amd64.tar.gz",
						Str:     "1.2.3-0-geae7e7d49",
					},
				},
				{
					Package: "tarantool-cluster-manager",
					Release: "1.3",
					Token:   "mock-token",
					Version: version.Version{
						Major:   1,
						Minor:   3,
						Patch:   0,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "g3857712a",
						Tarball: "tcm-1.3.0-0-g3857712a.linux.amd64.tar.gz",
						Str:     "1.3.0-0-g3857712a",
					},
				},
				{
					Package: "tarantool-cluster-manager",
					Release: "1.3",
					Token:   "mock-token",
					Version: version.Version{
						Major:   1,
						Minor:   3,
						Patch:   1,
						Release: version.Release{Type: version.TypeRelease},
						Hash:    "g074b5ffa",
						Tarball: "tcm-1.3.1-0-g074b5ffa.linux.amd64.tar.gz",
						Str:     "1.3.1-0-g074b5ffa",
					},
				},
			},
		},

		"tarantool_ee_debug_release_specific_versions": {
			program:         binary.ProgramEe,
			platform:        platformInfo{arch: "aarch64", os: binary.OsMacos},
			specificVersion: "3.2",
			searchDebug:     true,
			doerContent: map[string][]string{
				"3.2": {
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.macos.aarch64.tar.gz",
					"tarantool-enterprise-sdk-gc64-3.2.0-0-r40.macos.aarch64.tar.gz.sha256",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.macos.aarch64.tar.gz",
					"tarantool-enterprise-sdk-debug-gc64-3.2.0-0-r40.macos.aarch64.tar.gz.sha256",
				},
			},
			expectedQuery: "enterprise/release/macos/aarch64/3.2",
			expectedBundles: binary.BundleInfoSlice{
				{
					Package: "enterprise",
					Release: "3.2",
					Token:   "mock-token",
					Version: version.Version{
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
				},
			},
		},

		"unknown_os": {
			program:  binary.ProgramTcm,
			platform: platformInfo{arch: "x86_64", os: binary.OsUnknown},
			errMsg:   "unsupported OS",
		},

		"unsupported_arch": {
			program:  binary.ProgramTcm,
			platform: platformInfo{arch: "arm", os: binary.OsLinux},
			errMsg:   "unsupported architecture",
		},

		"unknown package": {
			program:  binary.ProgramCe,
			platform: platformInfo{arch: "arm", os: binary.OsLinux},
			errMsg:   "there is no tarantool.io package for program:",
		},

		"invalid version": {
			program:  binary.ProgramEe,
			platform: platformInfo{arch: "x86_64", os: binary.OsLinux},
			doerContent: map[string][]string{
				"3.1": {
					"tarantool-enterprise-sdk-gc64-Ver3.1.linux.x86_64.tar.gz",
				},
			},
			expectedQuery: "enterprise/release/linux/x86_64",
			errMsg:        `no packages found for this OS or release version`,
		},
		// spell-checker:enable
	}

	opts := config.CliOpts{
		Env: &config.TtEnvOpts{
			BinDir: "/test/bin",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			mockDoer := mockDoer{
				t:       t,
				content: tt.doerContent,
				query:   tt.expectedQuery,
			}

			sCtx := binary.NewSearchCtx(&tt.platform, &mockDoer)

			sCtx.Program = tt.program
			sCtx.ReleaseVersion = tt.specificVersion

			if tt.searchDebug {
				sCtx.Filter = binary.SearchDebug
			}

			bundles, err := binary.FetchBundlesInfo(&sCtx, &opts)

			if tt.errMsg != "" {
				require.Error(t, err, "Expected an error, but got nil")
				require.Contains(t, err.Error(), tt.errMsg,
					"Expected error message does not match")
			} else {
				require.NoError(t, err, "Expected no error, but got: %v", err)

				require.Equal(t, len(tt.expectedBundles), bundles.Len(),
					"Bundles length mismatch; got=%v", bundles)

				require.Equal(t, tt.expectedBundles, bundles, "Bundles mismatch")
			}
		})
	}
}

func TestSelectVersion(t *testing.T) {
	tests := map[string]struct {
		bs     binary.BundleInfoSlice
		ver    string
		want   string
		errMsg string
	}{
		"single version": {
			bs: binary.BundleInfoSlice{
				{Version: version.Version{Str: "1.0.0"}},
			},
			ver:  "1.0.0",
			want: "1.0.0",
		},

		"multiple version": {
			bs: binary.BundleInfoSlice{
				{Version: version.Version{Str: "1.0.0"}},
				{Version: version.Version{Str: "2.0.0"}},
				{Version: version.Version{Str: "3.0.0"}},
			},
			ver:  "2.0.0",
			want: "2.0.0",
		},

		"non existent version": {
			bs: binary.BundleInfoSlice{
				{Version: version.Version{Str: "1.0.0"}},
				{Version: version.Version{Str: "2.0.0"}},
			},
			ver:    "3.0.0",
			errMsg: `"3.0.0" version doesn't found`,
		},

		"empty bundle": {
			bs:     binary.BundleInfoSlice{},
			ver:    "1.0.0",
			errMsg: "no available versions",
		},

		"nil bundle": {
			bs:     nil,
			ver:    "1.0.0",
			errMsg: "no available versions",
		},

		"get last version": {
			bs: binary.BundleInfoSlice{
				{Version: version.Version{Str: "1.0.0"}},
				{Version: version.Version{Str: "2.0.0"}},
				{Version: version.Version{Str: "3.0.0"}},
			},
			ver:  "",
			want: "3.0.0",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := binary.SelectVersion(tt.bs, tt.ver)
			if tt.errMsg != "" {
				require.ErrorContains(t, err, tt.errMsg,
					"Expected error message does not match")

				return
			}

			require.NoError(t, err, "Expected no error, but got: %v", err)
			require.Equal(t, tt.want, got.Version.Str)
		})
	}
}
