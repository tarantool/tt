package pack

import (
	"fmt"
	"io"
	"net"
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

func TestRocksFinder(t *testing.T) {
	testDir := t.TempDir()
	badDir := "dir1/dir2/dir4"
	dirToCreate := "dir1/dir2/dir3/"
	pathToCreate := "dir1/dir2/dir3/myapp-scm-1.rockspec"
	pathToBeFound := filepath.Join(testDir, pathToCreate)

	testPaths := []string{
		pathToCreate,
	}
	dirsToCreate := []string{
		dirToCreate,
		badDir,
	}

	err := test_helpers.CreateDirs(testDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = test_helpers.CreateFiles(testDir, testPaths)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	resPath, err := findRocks(testDir)
	require.NoErrorf(t, err, "failed to find rocks: %v", err)
	require.Equalf(t, pathToBeFound, resPath,
		"failed to find rocks: incorrect path, expected: %v, got: %v",
		pathToBeFound, resPath)

	resPath, err = findRocks(badDir)
	require.NotNilf(t, err, "expected error to be returned, "+
		"instead err is %s and result path is %s",
		err, resPath)
	require.Equalf(t, "", resPath,
		"expected error to be returned, instead err is %s and result path is %s",
		err, resPath)
}

func TestCopyAppSrc(t *testing.T) {
	testDir := t.TempDir()
	testCopyDir := t.TempDir()

	filesToCreate := []string{
		"app1.lua",
		"app2/init.lua",
	}
	dirsToCreate := []string{
		"app2",
	}
	appLocations := []string{
		"app1.lua",
		"app2",
		"app3.lua",
	}

	expectedToExist := []string{
		"app1.lua",
		"app2/init.lua",
	}
	unExpectedToExist := []string{
		"app3.lua",
	}

	err := test_helpers.CreateDirs(testDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = test_helpers.CreateSymlink(filepath.Join(testDir, "app1.lua"), "app3.lua")
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	for _, name := range appLocations {
		err = copyAppSrc(filepath.Join(testDir, name), name, testCopyDir,
			skipArtifacts(&config.CliOpts{}))
		require.NoErrorf(t, err, "failed to copy an app: %v", err)
	}

	for _, path := range expectedToExist {
		require.FileExists(t, filepath.Join(testCopyDir, path))
	}

	for _, path := range unExpectedToExist {
		_, err := os.Stat(filepath.Join(testCopyDir, path))
		require.NotNilf(t, err, "didn't catch an expected error by checking: %s", path)
	}
}

func TestCopyBinaries(t *testing.T) {
	testDir := t.TempDir()
	testCopyDir := t.TempDir()

	filesToCreate := []string{
		"simple/tntExample",
		"link/tntLink",
		"brokenLink/tntLink",
		"missing/tntExample",
	}
	dirsToCreate := []string{
		"simple",
		"link",
		"brokenLink",
		"missing",
	}

	expectedToExist := []string{
		"simple/tarantool",
		"link/tarantool",
	}

	unExpectedToExist := []string{
		"missing/tarantool",
		"brokenLink/tarantool",
	}

	testCases := []string{
		"simple",
		"link",
		"brokenLink",
		"missing",
	}

	err := test_helpers.CreateDirs(testDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	for _, testCase := range testCases {
		t.Run(testCase, func(t *testing.T) {
			var cmdCtx cmdcontext.CmdCtx
			if testCase == "link" || testCase == "brokenLink" {
				err = test_helpers.CreateSymlink(filepath.Join(testDir,
					testCase, "tntLink"), "tntExample")
				require.NoErrorf(t, err, "failed to create test directories: %v", err)
			}

			cmdCtx.Cli.TarantoolCli.Executable = filepath.Join(testDir, testCase,
				"tntExample")

			tntPath := filepath.Join(testDir, testCase, "tntExample")

			if testCase == "link" || testCase == "brokenLink" {
				tntPath = filepath.Join(testDir, testCase, "tntLink")
			}

			tntScript := []byte("#!/bin/bash\nprintf 'Hello World'")
			err = os.WriteFile(tntPath, tntScript, 0644)
			require.NoErrorf(t, err, "failed to write to script: %v", err)

			cmd := exec.Command("sh", "-c",
				fmt.Sprintf("chmod +x %s", tntPath))
			err = cmd.Run()
			require.NoErrorf(t, err, "failed to make an executable: %v", err)

			if testCase == "missing" || testCase == "brokenLink" {
				err = os.Remove(tntPath)
				require.NoErrorf(t, err, "failed to remove the file: %v", err)
			}

			err = copyBinaries(cmdCtx.Cli.TarantoolCli, filepath.Join(testCopyDir, testCase))
			if testCase == "missing" {
				require.Equal(t, true, strings.Contains(err.Error(),
					"no such file or directory"))
			} else {
				require.NoErrorf(t, err, "failed to copy binaries: %v", err)
			}

			if testCase != "missing" && testCase != "brokenLink" {
				tntPath = filepath.Join(testCopyDir, testCase, "tarantool")

				out, err := exec.Command(tntPath).Output()
				require.NoErrorf(t, err, "failed to run tarantool binary: %v", err)

				require.Equal(t, []byte("Hello World"), out)
			}
		})
	}

	for _, path := range expectedToExist {
		require.FileExists(t, filepath.Join(testCopyDir, path))
	}

	for _, path := range unExpectedToExist {
		_, err := os.Stat(filepath.Join(testCopyDir, path))
		require.NotNilf(t, err, "didn't catch an expected error by checking: %s", path)
	}
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
					Directory: "modules",
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
					Directory: "modules",
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
					Directory: "modules",
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

func Test_skipArtifacts(t *testing.T) {
	cliOpts := config.CliOpts{App: &config.AppOpts{
		RunDir:   "./var/run",
		LogDir:   "var/log",
		MemtxDir: "/var/lib/tarantool",
		VinylDir: "var/lib/",
		WalDir:   "./var/lib/",
	}}

	tmpDir := t.TempDir()
	for _, dir := range []string{"var/run", "var/lib/tarantool", "var/log", "var/copy"} {
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, dir), 0755))
	}
	_, err := net.Listen("unix", filepath.Join(tmpDir, "tt.control"))
	require.NoError(t, err)

	shouldSkip := skipArtifacts(&cliOpts)

	tests := []struct {
		path     string
		wantErr  bool
		expected bool
	}{
		{
			path:     "var/run",
			wantErr:  false,
			expected: true,
		},
		{
			path:     "var/log",
			wantErr:  false,
			expected: true,
		},
		{
			path:     "var/lib/tarantool",
			wantErr:  false,
			expected: false,
		},
		{
			path:     "/var/lib/tarantool",
			wantErr:  false,
			expected: false,
		},
		{
			path:     "var/lib/",
			wantErr:  false,
			expected: true,
		},
		{
			path:     "./var/lib/",
			wantErr:  false,
			expected: true,
		},
		{
			path:     "./var/lib",
			wantErr:  false,
			expected: true,
		},
		{
			path:     "./var/not_exist",
			wantErr:  true,
			expected: false,
		},
		{
			path:     "./var/copy",
			wantErr:  false,
			expected: false,
		},
		{
			path:     "var/copy",
			wantErr:  false,
			expected: false,
		},
		{
			path:     "tt.control",
			wantErr:  false,
			expected: true, // Skip unix socket.
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := shouldSkip(filepath.Join(tmpDir, tt.path))
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, got)
		})
	}
}

func Test_prepareBundleBasic(t *testing.T) {
	cliOpts, configPath, err := configure.GetCliOpts("testdata/env1/tt.yaml",
		&mockRepository{})

	require.NoError(t, err)
	cmdCtx := cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			ConfigDir: filepath.Dir(configPath),
		},
		Integrity: integrity.IntegrityCtx{
			Repository: &mockRepository{},
		},
	}
	var packCtx PackCtx
	require.NoError(t, FillCtx(&cmdCtx, &packCtx, cliOpts, []string{"tgz"}))
	bundleDir, err := prepareBundle(&cmdCtx, &packCtx, cliOpts, false)
	require.NoError(t, err)
	defer func() {
		if strings.HasPrefix(bundleDir, "/tmp/") ||
			strings.HasPrefix(bundleDir, "/private/") {
			os.RemoveAll(bundleDir)
		}
	}()

	checks := []struct {
		checkFunc func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool
		path      string
	}{
		// Root.
		{assert.DirExists, "instances.enabled"},
		{assert.DirExists, "include"},
		{assert.DirExists, "modules"},
		{assert.FileExists, "tt.yaml"},

		// Single app.
		{assert.FileExists, "instances.enabled/single"},
		{assert.DirExists, "single/var"},
		{assert.FileExists, "single/init.lua"},
		{assert.NoDirExists, "single/var/lib"},
		{assert.NoDirExists, "single/var/log"},
		{assert.NoDirExists, "single/var/run"},

		// Multi-instance app.
		{assert.FileExists, "multi/init.lua"},
		{assert.FileExists, "multi/instances.yaml"},
		{assert.NoDirExists, "multi/var/lib"},
		{assert.NoDirExists, "multi/var/log"},
		{assert.NoDirExists, "multi/var/run"},

		// Script app.
		{assert.FileExists, "script.lua"},
		{assert.FileExists, "instances.enabled/script_app.lua"},
		{assert.DirExists, "instances.enabled/script_app"},
		{assert.NoFileExists, "instances.enabled/script_app/init.lua"},
		{assert.NoDirExists, "instances.enabled/script_app/var/lib"},
		{assert.NoDirExists, "instances.enabled/script_app/var/log"},
		{assert.NoDirExists, "instances.enabled/script_app/var/run"},
	}
	for _, check := range checks {
		check.checkFunc(t, filepath.Join(bundleDir, check.path))
	}

	cliOpts, _, err = configure.GetCliOpts(filepath.Join(bundleDir, "tt.yaml"),
		&mockRepository{})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(bundleDir, "instances.enabled"), cliOpts.Env.InstancesEnabled)
	assert.Equal(t, filepath.Join(bundleDir, "include"), cliOpts.Env.IncludeDir)
	assert.Equal(t, "var/lib", cliOpts.App.WalDir)
	assert.Equal(t, "var/lib", cliOpts.App.VinylDir)
	assert.Equal(t, "var/lib", cliOpts.App.MemtxDir)
	assert.Equal(t, "var/log", cliOpts.App.LogDir)
	assert.Equal(t, "var/run", cliOpts.App.RunDir)
}

func Test_prepareBundleWithArtifacts(t *testing.T) {
	cliOpts, configPath, err := configure.GetCliOpts("testdata/env1/tt.yaml",
		&mockRepository{})
	require.NoError(t, err)

	cmdCtx := cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			ConfigDir: filepath.Dir(configPath),
		},
		Integrity: integrity.IntegrityCtx{
			Repository: &mockRepository{},
		},
	}
	var packCtx PackCtx = PackCtx{
		Archive: ArchiveCtx{
			All: true,
		},
	}
	require.NoError(t, FillCtx(&cmdCtx, &packCtx, cliOpts, []string{"tgz"}))

	bundleDir, err := prepareBundle(&cmdCtx, &packCtx, cliOpts, false)
	require.NoError(t, err)
	defer func() {
		if strings.HasPrefix(bundleDir, "/tmp/") ||
			strings.HasPrefix(bundleDir, "/private/") {
			os.RemoveAll(bundleDir)
		}
	}()

	checks := []struct {
		checkFunc func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool
		path      string
	}{
		// Root.
		{assert.DirExists, "instances.enabled"},
		{assert.DirExists, "include"},
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
		{assert.FileExists, "instances.enabled/script_app.lua"},
		{assert.DirExists, "instances.enabled/script_app"},
		{assert.FileExists, "instances.enabled/script_app/var/lib/script_app/" +
			"00000000000000000000.snap"},
		{assert.FileExists, "instances.enabled/script_app/var/log/script_app/tt.log"},
		{assert.NoFileExists, "instances.enabled/script_app/init.lua"},
		{assert.NoDirExists, "instances.enabled/script_app/var/run"},
	}
	for _, check := range checks {
		check.checkFunc(t, filepath.Join(bundleDir, check.path))
	}
}

func Test_prepareBundleDifferentDataDirs(t *testing.T) {
	cliOpts, configPath, err := configure.GetCliOpts("testdata/env_different_dirs/tt.yaml",
		&mockRepository{})
	require.NoError(t, err)

	cmdCtx := cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			ConfigDir: filepath.Dir(configPath),
		},
		Integrity: integrity.IntegrityCtx{
			Repository: &mockRepository{},
		},
	}
	var packCtx PackCtx = PackCtx{
		Archive: ArchiveCtx{
			All: true,
		},
	}
	require.NoError(t, FillCtx(&cmdCtx, &packCtx, cliOpts, []string{"tgz"}))

	bundleDir, err := prepareBundle(&cmdCtx, &packCtx, cliOpts, false)
	require.NoError(t, err)
	defer func() {
		if strings.HasPrefix(bundleDir, "/tmp/") ||
			strings.HasPrefix(bundleDir, "/private/") {
			os.RemoveAll(bundleDir)
		}
	}()

	checks := []struct {
		checkFunc func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool
		path      string
	}{
		// Root.
		{assert.DirExists, "instances.enabled"},
		{assert.DirExists, "include"},
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
	}
	for _, check := range checks {
		check.checkFunc(t, filepath.Join(bundleDir, check.path))
	}
}

func Test_prepareBundleTntCtlLayout(t *testing.T) {
	cliOpts, configPath, err := configure.GetCliOpts("testdata/env_tntctl_layout/tt.yaml",
		&mockRepository{})
	require.NoError(t, err)

	cmdCtx := cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			ConfigDir: filepath.Dir(configPath),
		},
		Integrity: integrity.IntegrityCtx{
			Repository: &mockRepository{},
		},
	}
	var packCtx PackCtx = PackCtx{
		Archive: ArchiveCtx{
			All: true,
		},
	}
	require.NoError(t, FillCtx(&cmdCtx, &packCtx, cliOpts, []string{"tgz"}))

	bundleDir, err := prepareBundle(&cmdCtx, &packCtx, cliOpts, false)
	require.NoError(t, err)
	defer func() {
		if strings.HasPrefix(bundleDir, "/tmp/") ||
			strings.HasPrefix(bundleDir, "/private/") {
			os.RemoveAll(bundleDir)
		}
	}()

	checks := []struct {
		checkFunc func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool
		path      string
	}{
		// Root.
		{assert.DirExists, "instances.enabled"},
		{assert.DirExists, "include"},
		{assert.DirExists, "modules"},
		{assert.FileExists, "tt.yaml"},

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
	}
	for _, check := range checks {
		check.checkFunc(t, filepath.Join(bundleDir, check.path))
	}
}

func Test_prepareBundleCartridgeCompatWithArtifacts(t *testing.T) {
	cliOpts, configPath, err := configure.GetCliOpts("testdata/env1/tt.yaml",
		&mockRepository{})
	require.NoError(t, err)

	cmdCtx := cmdcontext.CmdCtx{
		Cli: cmdcontext.CliCtx{
			ConfigDir: filepath.Dir(configPath),
			TarantoolCli: cmdcontext.TarantoolCli{
				Executable: "testdata/env1/bin/tarantool",
			},
		},
		Integrity: integrity.IntegrityCtx{
			Repository: &mockRepository{},
		},
	}
	var packCtx PackCtx = PackCtx{
		AppList:         []string{"multi"},
		CartridgeCompat: true,
		Archive: ArchiveCtx{
			All: true,
		},
		TarantoolIsSystem: false,
	}
	require.NoError(t, FillCtx(&cmdCtx, &packCtx, cliOpts, []string{"tgz"}))

	bundleDir, err := prepareBundle(&cmdCtx, &packCtx, cliOpts, false)
	require.NoError(t, err)
	defer func() {
		if strings.HasPrefix(bundleDir, "/tmp/") ||
			strings.HasPrefix(bundleDir, "/private/") {
			os.RemoveAll(bundleDir)
		}
	}()

	checks := []struct {
		checkFunc func(t assert.TestingT, path string, msgAndArgs ...interface{}) bool
		path      string
	}{
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
	}
	for _, check := range checks {
		check.checkFunc(t, filepath.Join(bundleDir, check.path))
	}
}
