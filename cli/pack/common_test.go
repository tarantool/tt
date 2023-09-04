package pack

import (
	"fmt"
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
	"gopkg.in/yaml.v2"
)

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
		err = copyAppSrc(filepath.Join(testDir, name), name, testCopyDir)
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

func TestCopyArtifacts(t *testing.T) {
	testDir := t.TempDir()
	testPackageDir := t.TempDir()

	testOpts := &config.CliOpts{
		App: &config.AppOpts{
			WalDir:   filepath.Join(testDir, configure.VarDataPath),
			VinylDir: filepath.Join(testDir, configure.VarDataPath),
			MemtxDir: filepath.Join(testDir, configure.VarDataPath),
			LogDir:   filepath.Join(testDir, configure.VarLogPath),
			RunDir:   filepath.Join(testDir, configure.VarRunPath),
		},
	}

	var (
		appName      = "app1"
		dataArtifact = "test_file.data"
		logArtifact  = "test_file.log"
	)

	dirsToCreate := []string{
		filepath.Join(configure.VarDataPath, appName),
		filepath.Join(configure.VarRunPath, appName),
		filepath.Join(configure.VarLogPath, appName),
	}
	filesToCreate := []string{
		filepath.Join(configure.VarDataPath, appName, dataArtifact),
		filepath.Join(configure.VarLogPath, appName, logArtifact),
	}

	packageDirsToCreate := []string{
		filepath.Join(configure.VarDataPath, appName),
		filepath.Join(configure.VarRunPath, appName),
		filepath.Join(configure.VarLogPath, appName),
	}
	err := test_helpers.CreateDirs(testPackageDir, packageDirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = test_helpers.CreateDirs(testDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	prepareDefaultPackagePaths(testOpts, testPackageDir)
	err = copyArtifacts(testOpts, appName)
	require.NoErrorf(t, err, "failed to copy artifacts: %v", err)

	require.FileExists(t, filepath.Join(testPackageDir, configure.VarDataPath, appName,
		dataArtifact))
	require.FileExists(t, filepath.Join(testPackageDir, configure.VarLogPath, appName, logArtifact))
}

func TestCopyArtifactsCustomWalVinylMemtx(t *testing.T) {
	testDir := t.TempDir()
	testPackageDir := t.TempDir()

	testOpts := &config.CliOpts{
		App: &config.AppOpts{
			WalDir:   filepath.Join(testDir, configure.VarWalPath),
			VinylDir: filepath.Join(testDir, configure.VarVinylPath),
			MemtxDir: filepath.Join(testDir, configure.VarMemtxPath),
			LogDir:   filepath.Join(testDir, configure.VarLogPath),
			RunDir:   filepath.Join(testDir, configure.VarRunPath),
		},
	}

	var (
		appName      = "app1"
		dataArtifact = "test_file.data"
		logArtifact  = "test_file.log"
	)

	dirsToCreate := []string{
		filepath.Join(configure.VarWalPath, appName),
		filepath.Join(configure.VarVinylPath, appName),
		filepath.Join(configure.VarMemtxPath, appName),
		filepath.Join(configure.VarRunPath, appName),
		filepath.Join(configure.VarLogPath, appName),
	}
	filesToCreate := []string{
		filepath.Join(configure.VarWalPath, appName, dataArtifact),
		filepath.Join(configure.VarVinylPath, appName, dataArtifact),
		filepath.Join(configure.VarMemtxPath, appName, dataArtifact),
		filepath.Join(configure.VarLogPath, appName, logArtifact),
	}

	packageDirsToCreate := []string{
		filepath.Join(configure.VarWalPath, appName),
		filepath.Join(configure.VarVinylPath, appName),
		filepath.Join(configure.VarMemtxPath, appName),
		filepath.Join(configure.VarRunPath, appName),
		filepath.Join(configure.VarLogPath, appName),
	}
	err := test_helpers.CreateDirs(testPackageDir, packageDirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = test_helpers.CreateDirs(testDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	prepareDefaultPackagePaths(testOpts, testPackageDir)
	err = copyArtifacts(testOpts, appName)
	require.NoErrorf(t, err, "failed to copy artifacts: %v", err)

	require.FileExists(t, filepath.Join(testPackageDir, configure.VarVinylPath, appName,
		dataArtifact))
	require.FileExists(t, filepath.Join(testPackageDir, configure.VarWalPath, appName,
		dataArtifact))
	require.FileExists(t, filepath.Join(testPackageDir, configure.VarMemtxPath, appName,
		dataArtifact))
	require.FileExists(t, filepath.Join(testPackageDir, configure.VarLogPath, appName, logArtifact))
}

func TestCreateAppSymlink(t *testing.T) {
	testDir := t.TempDir()
	testPackageDir := t.TempDir()
	testOpts := &config.CliOpts{
		App: &config.AppOpts{}}
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

	prepareDefaultPackagePaths(testOpts, testPackageDir)
	err = createAppSymlink(filepath.Join(testDir, srcApp), appName)
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
				App: &config.AppOpts{InstancesEnabled: "../any_dir"},
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
				App: &config.AppOpts{InstancesEnabled: "."},
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
				App: &config.AppOpts{InstancesEnabled: "."},
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

func Test_createEnv(t *testing.T) {
	type args struct {
		opts     *config.CliOpts
		destPath string
	}

	testOptsStd := &config.CliOpts{
		App: &config.AppOpts{
			LogMaxBackups: 1,
			LogMaxAge:     1,
			LogMaxSize:    1,
			Restartable:   true,
			WalDir:        "test",
			VinylDir:      "test",
			MemtxDir:      "test",
		},
	}

	testOptsCustom := &config.CliOpts{
		App: &config.AppOpts{
			LogMaxBackups: 1,
			LogMaxAge:     1,
			LogMaxSize:    1,
			Restartable:   true,
			WalDir:        "test_wal",
			VinylDir:      "test_vinyl",
			MemtxDir:      "test_memtx",
		},
	}

	testDirStd := t.TempDir()
	testDirCustom := t.TempDir()

	tests := []struct {
		name      string
		testDir   string
		args      args
		wantErr   assert.ErrorAssertionFunc
		checkFunc func()
	}{
		{
			name: "Wal, Vinyl and Memtx directories are not separated",
			args: args{
				opts:     testOptsStd,
				destPath: testDirStd,
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			checkFunc: func() {
				cfg := &config.Config{}
				envFile, err := os.Open(filepath.Join(testDirStd, configure.ConfigName))
				require.NoErrorf(t, err, "failed to find a new created %s: %v",
					configure.ConfigName, err)

				defer envFile.Close()

				err = yaml.NewDecoder(envFile).Decode(cfg)
				require.NoErrorf(t, err, "failed to decode a new created %s: %v",
					configure.ConfigName, err)

				assert.Equalf(t, cfg.CliConfig.App.Restartable, testOptsStd.App.Restartable,
					"wrong restartable count")
				assert.Equalf(t, cfg.CliConfig.App.LogMaxAge, testOptsStd.App.LogMaxSize,
					"wrong log max age count")
				assert.Equalf(t, cfg.CliConfig.App.LogMaxSize, testOptsStd.App.LogMaxAge,
					"wrong log max size count")
				assert.Equalf(t, cfg.CliConfig.App.LogMaxBackups, testOptsStd.App.LogMaxBackups,
					"wrong log max backups count")
				assert.Equalf(t, cfg.CliConfig.App.InstancesEnabled,
					configure.InstancesEnabledDirName,
					"wrong instances enabled path")
				assert.Equalf(t, cfg.CliConfig.App.RunDir, configure.VarRunPath,
					"wrong run path")
				assert.Equalf(t, cfg.CliConfig.App.LogDir, configure.VarLogPath,
					"wrong log path")
				assert.Equalf(t, cfg.CliConfig.App.BinDir, configure.BinPath,
					"wrong bin path")
				assert.Equalf(t, cfg.CliConfig.App.WalDir, configure.VarDataPath,
					"wrong data path")
				assert.Equalf(t, cfg.CliConfig.App.VinylDir, configure.VarDataPath,
					"wrong data path")
				assert.Equalf(t, cfg.CliConfig.App.MemtxDir, configure.VarDataPath,
					"wrong data path")
				assert.Equalf(t, cfg.CliConfig.Modules.Directory, configure.ModulesPath,
					"wrong modules path")
			},
		},
		{
			name: "Wal, Vinyl and Memtx directories are separated",
			args: args{
				opts:     testOptsCustom,
				destPath: testDirCustom,
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return err == nil
			},
			checkFunc: func() {
				cfg := &config.Config{}
				envFile, err := os.Open(filepath.Join(testDirCustom, configure.ConfigName))
				require.NoErrorf(t, err, "failed to find a new created %s: %v",
					configure.ConfigName, err)

				defer envFile.Close()

				err = yaml.NewDecoder(envFile).Decode(cfg)
				require.NoErrorf(t, err, "failed to decode a new created %s: %v",
					configure.ConfigName, err)

				assert.Equalf(t, cfg.CliConfig.App.Restartable, testOptsCustom.App.Restartable,
					"wrong restartable count")
				assert.Equalf(t, cfg.CliConfig.App.LogMaxAge, testOptsCustom.App.LogMaxSize,
					"wrong log max age count")
				assert.Equalf(t, cfg.CliConfig.App.LogMaxSize, testOptsCustom.App.LogMaxAge,
					"wrong log max size count")
				assert.Equalf(t, cfg.CliConfig.App.LogMaxBackups,
					testOptsCustom.App.LogMaxBackups,
					"wrong log max backups count")
				assert.Equalf(t, cfg.CliConfig.App.InstancesEnabled,
					configure.InstancesEnabledDirName,
					"wrong instances enabled path")
				assert.Equalf(t, cfg.CliConfig.App.RunDir, configure.VarRunPath,
					"wrong run path")
				assert.Equalf(t, cfg.CliConfig.App.LogDir, configure.VarLogPath,
					"wrong log path")
				assert.Equalf(t, cfg.CliConfig.App.BinDir, configure.BinPath,
					"wrong bin path")
				assert.Equalf(t, cfg.CliConfig.App.WalDir, configure.VarWalPath,
					"wrong data path")
				assert.Equalf(t, cfg.CliConfig.App.VinylDir, configure.VarVinylPath,
					"wrong data path")
				assert.Equalf(t, cfg.CliConfig.App.MemtxDir, configure.VarMemtxPath,
					"wrong data path")
				assert.Equalf(t, cfg.CliConfig.Modules.Directory, configure.ModulesPath,
					"wrong modules path")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.wantErr(t, createEnv(tt.args.opts, tt.args.destPath, false),
				fmt.Sprintf("createEnv(%v, %v)", tt.args.opts, tt.args.destPath))
			tt.checkFunc()
		})
	}
}
