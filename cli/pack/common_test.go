package pack

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/lib/integrity"
)

type mockRepository struct{}

func (mock *mockRepository) Read(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (mock *mockRepository) ValidateAll() error {
	return nil
}

func TestGetVersion(t *testing.T) {
	testCases := []struct {
		name            string
		packCtx         *PackCtx
		opts            *config.CliOpts
		expectedVersion string
		defaultVersion  string
	}{
		{
			name: "Set version to pack context",
			packCtx: &PackCtx{
				Version: "1.0.0",
			},
			opts:            &config.CliOpts{Env: &config.TtEnvOpts{}},
			expectedVersion: "1.0.0",
			defaultVersion:  "",
		},
		{
			name: "Set custom version to pack context",
			packCtx: &PackCtx{
				Version: "v2",
			},
			opts:            &config.CliOpts{Env: &config.TtEnvOpts{}},
			defaultVersion:  "",
			expectedVersion: "v2",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			version := getVersion(testCase.packCtx, testCase.opts, testCase.defaultVersion)
			assert.Equalf(t, testCase.expectedVersion, version,
				"got unexpected version, expected: %s, actual: %s",
				testCase.expectedVersion, version)
		})
	}
}

func TestNormalizeGitVersion(t *testing.T) {
	testCases := []struct {
		name            string
		version         string
		expectedVersion string
	}{
		{
			name:            "Already normal",
			version:         "1.0.2-6",
			expectedVersion: "1.0.2.6",
		},
		{
			name:            "Missing count",
			version:         "1.0.2",
			expectedVersion: "1.0.2.0",
		},
		{
			name:            "Full version with hash",
			version:         "1.0.2-6-gc3bcd45",
			expectedVersion: "1.0.2.6",
		},
		{
			name:            "Full version with `v` symbol",
			version:         "v1.0.2-6-gc3bcd45",
			expectedVersion: "1.0.2.6",
		},
	}

	testCasesError := []struct {
		name    string
		version string
	}{
		{
			name:    "Extra number",
			version: "1.0.2.3-6",
		},
		{
			name:    "Incorrect count format",
			version: "1.0.2.3",
		},
		{
			name:    "Extra symbols",
			version: "vv1.0.2-6",
		},
		{
			name:    "Incorrect hash",
			version: "v1.0.2-6-1gc3bcd45",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			version, err := normalizeGitVersion(testCase.version)
			assert.Nil(t, err)
			assert.Equalf(t, testCase.expectedVersion, version,
				"got unexpected version, expected: %s, actual: %s",
				testCase.expectedVersion, version)
		})
	}
	for _, testCase := range testCasesError {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := normalizeGitVersion(testCase.version)
			assert.NotNilf(t, err, "expected error for input version: %s",
				testCase.version)
		})
	}
}

func Test_createNewOpts(t *testing.T) {
	type args struct {
		opts    *config.CliOpts
		packCtx PackCtx
	}

	testOptsStd := &config.CliOpts{
		Env: &config.TtEnvOpts{
			Restartable: true,
		},
		App: &config.AppOpts{
			WalDir:   "test",
			VinylDir: "test",
			MemtxDir: "test",
		},
	}

	tests := []struct {
		name        string
		testDir     string
		args        args
		wantErr     bool
		expectedOps *config.CliOpts
	}{
		{
			name: "Wal, Vinyl and Memtx directories are not separated",
			args: args{
				opts: testOptsStd,
				packCtx: PackCtx{
					Type: "tgz",
					Name: "bundle",
				},
			},
			expectedOps: &config.CliOpts{
				Env: &config.TtEnvOpts{
					BinDir:      "bin",
					IncludeDir:  "include",
					Restartable: true,
				},
				App: &config.AppOpts{
					WalDir:   "var/lib",
					VinylDir: "var/lib",
					MemtxDir: "var/lib",
					LogDir:   "var/log",
					RunDir:   "var/run",
				},
				Modules: &config.ModulesOpts{
					Directories: []string{"modules"},
				},
				Repo: &config.RepoOpts{
					Rocks:   "",
					Install: "distfiles",
				},
				EE: &config.EEOpts{},
				Templates: []config.TemplateOpts{
					{Path: "templates"},
				},
			},
		},
		{
			name: "System paths",
			args: args{
				opts: testOptsStd,
				packCtx: PackCtx{
					Type: "rpm",
					Name: "bundle",
				},
			},
			expectedOps: &config.CliOpts{
				Env: &config.TtEnvOpts{
					BinDir:      "bin",
					IncludeDir:  "include",
					Restartable: true,
				},
				App: &config.AppOpts{
					WalDir:   "/var/lib/tarantool/bundle",
					VinylDir: "/var/lib/tarantool/bundle",
					MemtxDir: "/var/lib/tarantool/bundle",
					LogDir:   "/var/log/tarantool/bundle",
					RunDir:   "/var/run/tarantool/bundle",
				},
				Modules: &config.ModulesOpts{
					Directories: []string{"modules"},
				},
				Repo: &config.RepoOpts{
					Rocks:   "",
					Install: "distfiles",
				},
				EE: &config.EEOpts{},
				Templates: []config.TemplateOpts{
					{Path: "templates"},
				},
			},
		},
		{
			name: "Single application env",
			args: args{
				opts: &config.CliOpts{
					Env: &config.TtEnvOpts{
						Restartable: true,
					},
					App: &config.AppOpts{},
				},
				packCtx: PackCtx{
					Type: "tgz",
					Name: "bundle",
				},
			},
			expectedOps: &config.CliOpts{
				Env: &config.TtEnvOpts{
					BinDir:      "bin",
					IncludeDir:  "include",
					Restartable: true,
				},
				App: &config.AppOpts{
					WalDir:   "var/lib",
					VinylDir: "var/lib",
					MemtxDir: "var/lib",
					LogDir:   "var/log",
					RunDir:   "var/run",
				},
				Modules: &config.ModulesOpts{
					Directories: []string{"modules"},
				},
				Repo: &config.RepoOpts{
					Rocks:   "",
					Install: "distfiles",
				},
				EE: &config.EEOpts{},
				Templates: []config.TemplateOpts{
					{Path: "templates"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createNewOpts(tt.args.opts, tt.args.packCtx)
			assert.Equal(t, tt.expectedOps, got)
		})
	}
}

func Test_prepareBundle(t *testing.T) {
	type params struct {
		configPath    string
		tntExecutable string
		tcmExecutable string
		packCtx       PackCtx
		build         bool
	}

	type check struct {
		checkFunc func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool
		path      string
	}

	tntExecutable, err := exec.LookPath("tarantool")
	require.NoError(t, err)

	tests := []struct {
		name    string
		params  params
		wantErr bool
		checks  []check
	}{
		{
			name: "Packing current application",
			params: params{
				configPath:    "testdata/single_app/tt.yml",
				tntExecutable: "testdata/single_app/bin/tarantool",
				packCtx:       PackCtx{TarantoolIsSystem: false},
			},
			wantErr: false,
			checks: []check{
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoFileExists, "tt.yml"},
				{assert.NoDirExists, "include"},

				{assert.NoDirExists, "single_app/include"},
				{assert.NoDirExists, "single_app/templates"},
				{assert.NoDirExists, "single_app/modules"},
				{assert.NoDirExists, "single_app/templates"},
				{assert.NoDirExists, "single_app/distfiles"},
				{assert.FileExists, "single_app/bin/tarantool"},
				{assert.FileExists, "single_app/bin/tt"},
				{assert.FileExists, "single_app/init.lua"},
				{assert.FileExists, "single_app/tt.yaml"},
				{assert.NoFileExists, "single_app/tt.yml"},
				{assert.NoFileExists, "single_app/single_app_0.1.0.0-1_x86_64.deb"},
				{assert.NoFileExists, "single_app/single_app-0.1.0.0-1.x86_64.rpm"},
				{assert.NoFileExists, "single_app/single_app-0.1.0.0.x86_64.tar.gz"},
				{assert.FileExists, "single_app/single_app-0.1.0.0.x86_64.zip"},
			},
		},
		{
			name: "Packing current application with changed name",
			params: params{
				configPath:    "testdata/single_app/tt.yml",
				tntExecutable: "testdata/single_app/bin/tarantool",
				packCtx:       PackCtx{TarantoolIsSystem: false, Name: "app"},
			},
			wantErr: false,
			checks: []check{
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoFileExists, "tt.yml"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "single_app"},

				{assert.NoDirExists, "app/include"},
				{assert.NoDirExists, "app/templates"},
				{assert.NoDirExists, "app/modules"},
				{assert.NoDirExists, "app/templates"},
				{assert.NoDirExists, "app/distfiles"},
				{assert.FileExists, "app/bin/tarantool"},
				{assert.FileExists, "app/bin/tt"},
				{assert.FileExists, "app/init.lua"},
				{assert.FileExists, "app/tt.yaml"},
				{assert.NoFileExists, "app/tt.yml"},
				{assert.FileExists, "app/single_app_0.1.0.0-1_x86_64.deb"},
				{assert.FileExists, "app/single_app-0.1.0.0-1.x86_64.rpm"},
				{assert.FileExists, "app/single_app-0.1.0.0.x86_64.tar.gz"},
				{assert.FileExists, "app/single_app-0.1.0.0.x86_64.zip"},
			},
		},
		{
			name: "Packing current application without binaries",
			params: params{
				configPath:    "testdata/single_app/tt.yml",
				tntExecutable: "testdata/single_app/bin/tarantool",
				packCtx:       PackCtx{WithoutBinaries: true},
			},
			wantErr: false,
			checks: []check{
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "single_app/include"},
				{assert.NoDirExists, "single_app/templates"},
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoDirExists, "single_app/modules"},
				{assert.NoFileExists, "single_app/bin/tarantool"},
				{assert.NoFileExists, "single_app/bin/tt"},
				{assert.FileExists, "single_app/init.lua"},
				{assert.FileExists, "single_app/tt.yaml"},
			},
		},
		{
			name: "Packing current application without modules",
			params: params{
				configPath:    "testdata/single_app_no_modules/tt.yaml",
				tntExecutable: "testdata/single_app_no_modules/bin/tarantool",
				packCtx: PackCtx{
					WithoutBinaries: true,
				},
			},
			wantErr: false,
			checks: []check{
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "single_app_no_modules/include"},
				{assert.NoDirExists, "single_app_no_modules/templates"},
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoDirExists, "single_app_no_modules/modules"},
				{assert.NoFileExists, "single_app_no_modules/bin/tarantool"},
				{assert.NoFileExists, "single_app_no_modules/bin/tt"},
				{assert.FileExists, "single_app_no_modules/init.lua"},
				{assert.FileExists, "single_app_no_modules/tt.yaml"},
			},
		},
		{
			name: "Packing current application without tarantool",
			params: params{
				configPath: "testdata/single_app_no_binaries/tt.yaml",
				packCtx:    PackCtx{},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},

				// App sub-dir.
				{assert.NoDirExists, "single_app_no_binaries/include"},
				{assert.NoDirExists, "single_app_no_binaries/templates"},
				{assert.NoDirExists, "single_app_no_binaries/modules"},
				{assert.FileExists, "single_app_no_binaries/bin/tt"},
				{assert.FileExists, "single_app_no_binaries/init.lua"},
				{assert.FileExists, "single_app_no_binaries/tt.yaml"},
			},
		},
		{
			name: "Packing app and build rocks",
			params: params{
				configPath:    "testdata/app_with_rockspec/tt.yaml",
				tntExecutable: tntExecutable,
				packCtx:       PackCtx{},
				build:         true,
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},

				// App sub-dir.
				{assert.NoDirExists, "app_with_rockspec/include"},
				{assert.NoDirExists, "app_with_rockspec/templates"},
				{assert.NoDirExists, "app_with_rockspec/modules"},
				{assert.DirExists, "app_with_rockspec/.rocks"},
				{assert.FileExists, "app_with_rockspec/bin/tt"},
				{assert.FileExists, "app_with_rockspec/init.lua"},
				{assert.FileExists, "app_with_rockspec/tt.yaml"},

				// No build files.
				{assert.NoFileExists, "app_with_rockspec/app_with_rockspec-scm-1.rockspec"},
				{assert.NoFileExists, "app_with_rockspec/tt.pre-build"},
				{assert.NoFileExists, "app_with_rockspec/tt.post-build"},
			},
		},
		{
			name: "Packing app, build rocks and rename",
			params: params{
				configPath:    "testdata/app_with_rockspec/tt.yaml",
				tntExecutable: tntExecutable,
				packCtx:       PackCtx{Name: "app"},
				build:         true,
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoDirExists, "app_with_rockspec"},

				// App sub-dir.
				{assert.NoDirExists, "app/include"},
				{assert.NoDirExists, "app/templates"},
				{assert.NoDirExists, "app/modules"},
				{assert.DirExists, "app/.rocks"},
				{assert.FileExists, "app/bin/tt"},
				{assert.FileExists, "app/init.lua"},
				{assert.FileExists, "app/tt.yaml"},

				// No build files.
				{assert.NoFileExists, "app/app_with_rockspec-scm-1.rockspec"},
				{assert.NoFileExists, "app/tt.pre-build"},
				{assert.NoFileExists, "app/tt.post-build"},
			},
		},
		{
			name: "Broken binary symlink",
			params: params{
				configPath:    "testdata/broken_binary_symlink/tt.yaml",
				tntExecutable: "testdata/broken_binary_symlink/bin/tarantool",
				packCtx:       PackCtx{},
				build:         false,
			},
			wantErr: true,
			checks:  []check{},
		},
		{
			name: "Packing app with change package name",
			params: params{
				configPath:    "testdata/app_with_rockspec/tt.yaml",
				tntExecutable: tntExecutable,
				packCtx:       PackCtx{Name: "app"},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},

				// App sub-dir.
				{assert.NoDirExists, "app/include"},
				{assert.NoDirExists, "app/templates"},
				{assert.NoDirExists, "app/modules"},
				{assert.NoDirExists, "app_with_rockspec/.rocks"},
				{assert.FileExists, "app/bin/tt"},
				{assert.FileExists, "app/init.lua"},
				{assert.FileExists, "app/tt.yaml"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.FileExists(t, tt.params.configPath)
			cliOpts, configPath, err := configure.GetCliOpts(
				tt.params.configPath, &mockRepository{})
			require.NoError(t, err)

			cmdCtx := cmdcontext.CmdCtx{
				Cli: cmdcontext.CliCtx{
					ConfigDir: filepath.Dir(configPath),
					TarantoolCli: cmdcontext.TarantoolCli{
						Executable: tt.params.tntExecutable,
					},
					TcmCli: cmdcontext.TcmCli{
						Executable: tt.params.tcmExecutable,
					},
					ConfigPath: configPath,
				},
				Integrity: integrity.IntegrityCtx{Repository: &mockRepository{}},
			}
			err = FillCtx(&cmdCtx, &tt.params.packCtx, cliOpts, []string{"tgz"})
			if tt.wantErr && err != nil {
				return
			}
			require.NoError(t, err)

			bundleDir, err := prepareBundle(&cmdCtx, &tt.params.packCtx, cliOpts, tt.params.build)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			defer func() {
				if strings.HasPrefix(bundleDir, "/tmp/") ||
					strings.HasPrefix(bundleDir, "/private/") {

					os.RemoveAll(bundleDir)
				}
			}()

			for _, check := range tt.checks {
				check.checkFunc(t, filepath.Join(bundleDir, check.path))
			}
		})
	}
}
