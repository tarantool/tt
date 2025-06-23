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
	"github.com/tarantool/tt/cli/pack/test_helpers"
	"github.com/tarantool/tt/lib/integrity"
)

type mockRepository struct{}

func (mock *mockRepository) Read(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (mock *mockRepository) ValidateAll() error {
	return nil
}

func TestCreateAppSymlink(t *testing.T) {
	testDir := t.TempDir()
	testPackageDir := t.TempDir()
	var (
		srcApp  = "app1.lua"
		appName = "app1_link"
	)
	filesToCreate := []string{
		srcApp,
	}
	dirsToCreate := []string{
		configure.InstancesEnabledDirName,
	}

	err := test_helpers.CreateDirs(testPackageDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateFiles(testPackageDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateSymlink(filepath.Join(testDir, srcApp), appName+".lua")
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = createAppSymlink(filepath.Join(testDir, srcApp), appName,
		filepath.Join(testPackageDir, configure.InstancesEnabledDirName))
	require.NoErrorf(t, err, "failed to create a symlink: %v", err)

	_, err = os.Lstat(filepath.Join(testPackageDir, configure.InstancesEnabledDirName, appName))
	require.NoErrorf(t, err, "failed to find a symlink: %v", err)
	resolvedPath, err := filepath.EvalSymlinks(filepath.Join(testPackageDir,
		configure.InstancesEnabledDirName, appName))
	require.NoErrorf(t, err, "failed to resolve a symlink: %v", err)
	require.Equalf(t, srcApp, filepath.Base(resolvedPath),
		"wrong created symlink: points to %s", srcApp)
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
			name:    "No parameters in context",
			packCtx: &PackCtx{},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: "../any_dir"},
			},
			expectedVersion: defaultLongVersion,
			defaultVersion:  defaultLongVersion,
		},
		{
			name: "Set version to pack context",
			packCtx: &PackCtx{
				Version: "1.0.0",
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: "."},
			},
			expectedVersion: "1.0.0",
			defaultVersion:  "",
		},
		{
			name: "Set custom version to pack context",
			packCtx: &PackCtx{
				Version: "v2",
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{InstancesEnabled: "."},
			},
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

	testOptsCustom := &config.CliOpts{
		Env: &config.TtEnvOpts{
			Restartable:        true,
			TarantoolctlLayout: true,
		},
		App: &config.AppOpts{
			WalDir:   "test_wal",
			VinylDir: "test_vinyl",
			MemtxDir: "test_memtx",
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
					BinDir:             "bin",
					IncludeDir:         "include",
					InstancesEnabled:   configure.InstancesEnabledDirName,
					Restartable:        true,
					TarantoolctlLayout: false,
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
			name: "Wal, Vinyl and Memtx directories are separated",
			args: args{
				opts: testOptsCustom,
				packCtx: PackCtx{
					Type: "tgz",
					Name: "bundle",
				},
			},
			expectedOps: &config.CliOpts{
				Env: &config.TtEnvOpts{
					BinDir:             "bin",
					IncludeDir:         "include",
					InstancesEnabled:   configure.InstancesEnabledDirName,
					Restartable:        true,
					TarantoolctlLayout: true,
				},
				App: &config.AppOpts{
					WalDir:   "var/wal",
					VinylDir: "var/vinyl",
					MemtxDir: "var/snap",
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
					BinDir:             "bin",
					IncludeDir:         "include",
					InstancesEnabled:   configure.InstancesEnabledDirName,
					Restartable:        true,
					TarantoolctlLayout: false,
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
						Restartable:      true,
						InstancesEnabled: ".",
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
					BinDir:             "bin",
					IncludeDir:         "include",
					InstancesEnabled:   ".",
					Restartable:        true,
					TarantoolctlLayout: false,
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
			name: "Default packing multiple applications",
			params: params{
				configPath:    "testdata/env1/tt.yaml",
				tntExecutable: "testdata/env1/bin/tarantool",
				tcmExecutable: "testdata/env1/bin/tcm",
				packCtx: PackCtx{
					WithBinaries: true,
				},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.DirExists, "instances.enabled"},
				{assert.FileExists, "bin/tarantool"},
				{assert.FileExists, "bin/tt"},
				{assert.FileExists, "bin/tcm"},
				{assert.NoDirExists, "include"},
				{assert.DirExists, "modules"},
				{assert.FileExists, "tt.yaml"},

				// Single app.
				{assert.FileExists, "instances.enabled/single"},
				{assert.DirExists, "single/var"},
				{assert.FileExists, "single/init.lua"},
				{assert.NoDirExists, "single/var/lib"},
				{assert.NoDirExists, "single/var/log"},
				{assert.NoDirExists, "single/var/run"},
				{func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool {
					assert.FileExists(t, path)
					stat, err := os.Lstat(path)
					if assert.NoError(t, err) {
						assert.NotZero(t, stat.Mode()&os.ModeSymlink)
						target, err := os.Readlink(path)
						assert.NoError(t, err)
						assert.Equal(t, "../single", target)
					}
					return true
				}, "instances.enabled/single"},

				// Multi-instance app.
				{assert.FileExists, "multi/init.lua"},
				{assert.FileExists, "multi/instances.yaml"},
				{assert.NoDirExists, "multi/var/lib"},
				{assert.NoDirExists, "multi/var/log"},
				{assert.NoDirExists, "multi/var/run"},
				{func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool {
					assert.FileExists(t, path)
					stat, err := os.Lstat(path)
					if assert.NoError(t, err) {
						assert.NotZero(t, stat.Mode()&os.ModeSymlink)
						target, err := os.Readlink(path)
						assert.NoError(t, err)
						assert.Equal(t, "../multi", target)
					}
					return true
				}, "instances.enabled/multi"},

				// Script app.
				{assert.FileExists, "script.lua"},
				{assert.NoFileExists, "script_app.lua"},
				{func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool {
					assert.FileExists(t, path)
					stat, err := os.Lstat(path)
					if assert.NoError(t, err) {
						assert.NotZero(t, stat.Mode()&os.ModeSymlink)
						target, err := os.Readlink(path)
						assert.NoError(t, err)
						assert.Equal(t, "../script.lua", target)
					}
					return true
				}, "instances.enabled/script_app.lua"},

				{assert.DirExists, "instances.enabled/script_app"},
				{assert.NoFileExists, "instances.enabled/script_app/init.lua"},
				{assert.NoDirExists, "instances.enabled/script_app/var/lib"},
				{assert.NoDirExists, "instances.enabled/script_app/var/log"},
				{assert.NoDirExists, "instances.enabled/script_app/var/run"},

				{
					func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool {
						cliOpts, _, err := configure.GetCliOpts(filepath.Join(path, "tt.yaml"),
							&mockRepository{})
						if assert.NoError(t, err) {
							assert.Equal(t, filepath.Join(path, "instances.enabled"),
								cliOpts.Env.InstancesEnabled)
							assert.Equal(t, filepath.Join(path, "include"), cliOpts.Env.IncludeDir)
							assert.Equal(t, "var/lib", cliOpts.App.WalDir)
							assert.Equal(t, "var/lib", cliOpts.App.VinylDir)
							assert.Equal(t, "var/lib", cliOpts.App.MemtxDir)
							assert.Equal(t, "var/log", cliOpts.App.LogDir)
							assert.Equal(t, "var/run", cliOpts.App.RunDir)
						}
						return true
					},
					".",
				},
			},
		},
		{
			name: "Packing multiple applications with artifacts",
			params: params{
				configPath:    "testdata/env1/tt.yaml",
				tntExecutable: "testdata/env1/bin/tarantool",
				packCtx: PackCtx{
					Archive: ArchiveCtx{
						All: true,
					},
				},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.DirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.DirExists, "modules"},
				{assert.FileExists, "tt.yaml"},

				// Single app.
				{assert.FileExists, "instances.enabled/single"},
				{assert.DirExists, "single/var"},
				{assert.FileExists, "single/init.lua"},
				{assert.FileExists, "single/var/lib/single/00000000000000000000.snap"},
				{assert.FileExists, "single/var/lib/single/00000000000000000000.xlog"},
				{assert.FileExists, "single/var/log/single/tt.log"},
				{assert.NoDirExists, "single/var/run"},

				// Multi-instance app.
				{assert.FileExists, "multi/init.lua"},
				{assert.FileExists, "multi/instances.yaml"},
				{assert.FileExists, "multi/var/lib/inst1/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/lib/inst1/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/lib/inst2/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/lib/inst2/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/log/inst1/tt.log"},
				{assert.FileExists, "multi/var/log/inst2/tt.log"},
				{assert.NoDirExists, "multi/var/run"},

				// Script app.
				{assert.FileExists, "script.lua"},
				{assert.NoFileExists, "script_app.lua"},
				{assert.FileExists, "instances.enabled/script_app.lua"},
				{assert.DirExists, "instances.enabled/script_app"},
				{assert.FileExists, "instances.enabled/script_app/var/lib/script_app/" +
					"00000000000000000000.snap"},
				{assert.FileExists, "instances.enabled/script_app/var/log/script_app/tt.log"},
				{assert.NoFileExists, "instances.enabled/script_app/init.lua"},
				{assert.NoDirExists, "instances.enabled/script_app/var/run"},
			},
		},
		{
			name: "Packing multiple applications with different data directories",
			params: params{
				configPath:    "testdata/env_different_dirs/tt.yaml",
				tntExecutable: "testdata/env1/bin/tarantool",
				packCtx: PackCtx{
					Archive: ArchiveCtx{
						All: true,
					},
				},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.DirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.DirExists, "modules"},
				{assert.FileExists, "tt.yaml"},

				// Single app.
				{assert.FileExists, "instances.enabled/single"},
				{assert.FileExists, "single/init.lua"},
				{assert.FileExists, "single/var/snap/single/00000000000000000000.snap"},
				{assert.FileExists, "single/var/wal/single/00000000000000000000.xlog"},
				{assert.DirExists, "single/var/vinyl/single/"},
				{assert.FileExists, "single/var/log/single/tt.log"},
				{assert.NoDirExists, "single/var/run"},

				// Multi-instance app.
				{assert.FileExists, "instances.enabled/multi"},
				{assert.FileExists, "multi/init.lua"},
				{assert.FileExists, "multi/instances.yaml"},
				{assert.FileExists, "multi/var/snap/inst1/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/snap/inst2/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/wal/inst1/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/wal/inst2/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/log/inst1/tt.log"},
				{assert.FileExists, "multi/var/log/inst2/tt.log"},
				{assert.NoDirExists, "multi/var/run"},

				// Script app.
				{assert.FileExists, "script.lua"},
				{assert.FileExists, "instances.enabled/script_app.lua"},
				{assert.FileExists, "instances.enabled/script_app/var/snap/script_app/" +
					"00000000000000000000.snap"},
				{assert.DirExists, "instances.enabled/script_app/var/wal"},
				{assert.DirExists, "instances.enabled/script_app/var/vinyl"},
				{assert.FileExists, "instances.enabled/script_app/var/log/script_app/tt.log"},
				{assert.NoFileExists, "instances.enabled/script_app/init.lua"},
				{assert.NoDirExists, "instances.enabled/script_app/var/run"},
			},
		},
		{
			name: "Packing with tarantoolctl layout",
			params: params{
				configPath: "testdata/env_tntctl_layout/tt.yaml",
				packCtx: PackCtx{
					Archive: ArchiveCtx{
						All: true,
					},
				},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.DirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.DirExists, "modules"},
				{assert.FileExists, "tt.yaml"},
				{assert.FileExists, "bin/tt"},
				{assert.NoFileExists, "bin/tarantool"},

				// Multi-instance app.
				{assert.FileExists, "instances.enabled/multi"},
				{assert.FileExists, "multi/init.lua"},
				{assert.FileExists, "multi/instances.yaml"},
				{assert.FileExists, "multi/var/lib/inst1/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/lib/inst1/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/lib/inst2/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/lib/inst2/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/log/inst1/tt.log"},
				{assert.FileExists, "multi/var/log/inst2/tt.log"},
				{assert.NoDirExists, "multi/var/run"},

				// Single app.
				{assert.DirExists, "var/lib/single"},
				{assert.FileExists, "instances.enabled/single"},
				{assert.FileExists, "single/init.lua"},
				{assert.FileExists, "var/lib/single/00000000000000000000.snap"},
				{assert.FileExists, "var/lib/single/00000000000000000000.xlog"},
				{assert.FileExists, "var/log/single.log"},
				{assert.NoDirExists, "var/run"},

				// Script app.
				{assert.DirExists, "var/lib/script_app"},
				{assert.FileExists, "script.lua"},
				{assert.FileExists, "instances.enabled/script_app.lua"},
				{assert.FileExists, "var/lib/script_app/00000000000000000000.snap"},
				{assert.FileExists, "var/lib/script_app/00000000000000000000.xlog"},
				{assert.FileExists, "var/log/script_app.log"},
				// Working dir must be created for script-file app.
				{assert.DirExists, "instances.enabled/script_app/"},
			},
		},
		{
			name: "Packing cartridge compat",
			params: params{
				configPath:    "testdata/env1/tt.yaml",
				tntExecutable: "testdata/env1/bin/tarantool",
				packCtx: PackCtx{
					AppList:           []string{"multi"},
					CartridgeCompat:   true,
					Archive:           ArchiveCtx{},
					TarantoolIsSystem: false,
				},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "modules"},
				{assert.NoDirExists, "var"},
				{assert.NoFileExists, "tt.yaml"},

				// Multi-inst app.
				{assert.DirExists, "multi"},
				{assert.FileExists, "multi/init.lua"},
				{assert.FileExists, "multi/instances.yaml"},
				{assert.FileExists, "multi/tt.yaml"},
				{assert.FileExists, "multi/tarantool"},
				{assert.FileExists, "multi/tt"},
				{assert.NoDirExists, "multi/var/lib/"},
				{assert.NoDirExists, "multi/var/log/"},
			},
		},
		{
			name: "Packing cartridge compat, no tarantool executable",
			params: params{
				configPath:    "testdata/env1/tt.yaml",
				tntExecutable: "",
				packCtx: PackCtx{
					AppList:           []string{"multi"},
					CartridgeCompat:   true,
					Archive:           ArchiveCtx{},
					TarantoolIsSystem: false,
				},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "modules"},
				{assert.NoDirExists, "var"},
				{assert.NoFileExists, "tt.yaml"},

				// Multi-inst app.
				{assert.DirExists, "multi"},
				{assert.FileExists, "multi/init.lua"},
				{assert.FileExists, "multi/instances.yaml"},
				{assert.FileExists, "multi/tt.yaml"},
				{assert.NoFileExists, "multi/tarantool"},
				{assert.NoDirExists, "multi/tarantool"},
				{assert.FileExists, "multi/tt"},
				{assert.NoDirExists, "multi/var/lib/"},
				{assert.NoDirExists, "multi/var/log/"},
			},
		},
		{
			name: "Packing cartridge compat with artifacts",
			params: params{
				configPath:    "testdata/env1/tt.yaml",
				tntExecutable: "testdata/env1/bin/tarantool",
				packCtx: PackCtx{
					AppList:         []string{"multi"},
					CartridgeCompat: true,
					Archive: ArchiveCtx{
						All: true,
					},
					TarantoolIsSystem: false,
				},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "modules"},
				{assert.NoDirExists, "var"},
				{assert.NoFileExists, "tt.yaml"},

				// Multi-inst app.
				{assert.DirExists, "multi"},
				{assert.FileExists, "multi/init.lua"},
				{assert.FileExists, "multi/instances.yaml"},
				{assert.FileExists, "multi/tt.yaml"},
				{assert.FileExists, "multi/tarantool"},
				{assert.FileExists, "multi/var/lib/inst1/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/lib/inst1/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/lib/inst2/00000000000000000000.snap"},
				{assert.FileExists, "multi/var/lib/inst2/00000000000000000000.xlog"},
				{assert.FileExists, "multi/var/log/inst1/tt.log"},
				{assert.FileExists, "multi/var/log/inst2/tt.log"},
			},
		},
		{
			name: "Packing env with instances.enabled:.",
			params: params{
				configPath:    "testdata/single_app/tt.yml",
				tntExecutable: "testdata/single_app/bin/tarantool",
				packCtx:       PackCtx{TarantoolIsSystem: false},
			},
			wantErr: false,
			checks: []check{
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoFileExists, "tt.yml"},
				{assert.NoDirExists, "include"},

				{assert.NoDirExists, "single_app/instances.enabled"},
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
			name: "Packing env with instances.enabled:. and changed name",
			params: params{
				configPath:    "testdata/single_app/tt.yml",
				tntExecutable: "testdata/single_app/bin/tarantool",
				packCtx:       PackCtx{TarantoolIsSystem: false, Name: "app"},
			},
			wantErr: false,
			checks: []check{
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoFileExists, "tt.yml"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "single_app"},

				{assert.NoDirExists, "app/instances.enabled"},
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
			name: "Packing env with instances.enabled:., without binaries",
			params: params{
				configPath:    "testdata/single_app/tt.yml",
				tntExecutable: "testdata/single_app/bin/tarantool",
				packCtx:       PackCtx{WithoutBinaries: true},
			},
			wantErr: false,
			checks: []check{
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "single_app/instances.enabled"},
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
			name: "Packing env with instances.enabled:., no modules",
			params: params{
				configPath:    "testdata/single_app_no_modules/tt.yaml",
				tntExecutable: "testdata/single_app_no_modules/bin/tarantool",
				packCtx: PackCtx{
					WithoutBinaries: true,
				},
			},
			wantErr: false,
			checks: []check{
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "single_app_no_modules/instances.enabled"},
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
			name: "Packing env with instances.enabled:. and no tarantool",
			params: params{
				configPath: "testdata/single_app_no_binaries/tt.yaml",
				packCtx:    PackCtx{},
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},

				// App sub-dir.
				{assert.NoDirExists, "single_app_no_binaries/instances.enabled"},
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
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},

				// App sub-dir.
				{assert.NoDirExists, "app_with_rockspec/instances.enabled"},
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
				{assert.NoFileExists, "app_with_rockspec/cartridge.pre-build"},
				{assert.NoFileExists, "app_with_rockspec/cartridge.post-build"},
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
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},
				{assert.NoDirExists, "app_with_rockspec"},

				// App sub-dir.
				{assert.NoDirExists, "app/instances.enabled"},
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
				{assert.NoFileExists, "app/cartridge.pre-build"},
				{assert.NoFileExists, "app/cartridge.post-build"},
			},
		},
		{
			name: "Packing 2 apps and build rocks in one only",
			params: params{
				configPath:    "testdata/only_one_app_buildable/tt.yaml",
				tntExecutable: tntExecutable,
				packCtx:       PackCtx{},
				build:         true,
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.DirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "modules"},
				{assert.FileExists, "bin/tt"},
				{assert.FileExists, "tt.yaml"},

				// App1.
				{assert.DirExists, "app1/.rocks"},
				{assert.FileExists, "app1/init.lua"},

				// App2.
				{assert.NoDirExists, "app2/.rocks"},
				{assert.FileExists, "app2/init.lua"},
			},
		},
		{
			name: "Packing 2 apps and skip building",
			params: params{
				configPath:    "testdata/only_one_app_buildable/tt.yaml",
				tntExecutable: tntExecutable,
				packCtx:       PackCtx{},
				build:         false,
			},
			wantErr: false,
			checks: []check{
				// Root.
				{assert.DirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "modules"},
				{assert.FileExists, "bin/tt"},
				{assert.FileExists, "tt.yaml"},

				// App1.
				{assert.NoDirExists, "app1/.rocks"},
				{assert.FileExists, "app1/init.lua"},

				// App2.
				{assert.NoDirExists, "app2/.rocks"},
				{assert.FileExists, "app2/init.lua"},
			},
		},
		{
			name: "Broken application symlink",
			params: params{
				configPath:    "testdata/broken_app_symlink/tt.yaml",
				tntExecutable: tntExecutable,
				packCtx:       PackCtx{},
				build:         false,
			},
			checks: []check{
				// Root.
				{assert.NoFileExists, "instances.enabled/app1"},
				{assert.FileExists, "instances.enabled/app2"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "modules"},
				{assert.FileExists, "bin/tt"},
				{assert.FileExists, "tt.yaml"},

				// App1 is skipped due to broken symlink.
				{assert.NoDirExists, "app1"},

				// App2.
				{assert.FileExists, "app2/init.lua"},
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
				{assert.NoDirExists, "instances.enabled"},
				{assert.NoDirExists, "include"},
				{assert.NoDirExists, "bin"},
				{assert.NoFileExists, "tt.yaml"},

				// App sub-dir.
				{assert.NoDirExists, "app/instances.enabled"},
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
			require.NoError(t, FillCtx(&cmdCtx, &tt.params.packCtx, cliOpts, []string{"tgz"}))
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
