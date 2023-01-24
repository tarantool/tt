package pack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCreateEnv(t *testing.T) {
	testDir := t.TempDir()

	testOpts := &config.CliOpts{
		App: &config.AppOpts{
			LogMaxBackups: 1,
			LogMaxAge:     1,
			LogMaxSize:    1,
			Restartable:   true,
		},
	}

	err := createEnv(testOpts, testDir)
	require.NoErrorf(t, err, "failed to create a new tarantool env file: %v", err)

	cfg := &config.Config{}
	envFile, err := os.Open(filepath.Join(testDir, configure.ConfigName))
	require.NoErrorf(t, err, "failed to find a new created %s: %v", configure.ConfigName, err)

	defer envFile.Close()

	err = yaml.NewDecoder(envFile).Decode(cfg)
	require.NoErrorf(t, err, "failed to decode a new created %s: %v", configure.ConfigName, err)

	assert.Equalf(t, cfg.CliConfig.App.Restartable, testOpts.App.Restartable,
		"wrong restartable count")
	assert.Equalf(t, cfg.CliConfig.App.LogMaxAge, testOpts.App.LogMaxSize,
		"wrong log max age count")
	assert.Equalf(t, cfg.CliConfig.App.LogMaxSize, testOpts.App.LogMaxAge,
		"wrong log max size count")
	assert.Equalf(t, cfg.CliConfig.App.LogMaxBackups, testOpts.App.LogMaxBackups,
		"wrong log max backups count")
	assert.Equalf(t, cfg.CliConfig.App.InstancesEnabled, instancesEnabledPath,
		"wrong instances enabled path")
	assert.Equalf(t, cfg.CliConfig.App.RunDir, filepath.Join(varPath, runPath),
		"wrong run path")
	assert.Equalf(t, cfg.CliConfig.App.LogDir, filepath.Join(varPath, logPath),
		"wrong log path")
	assert.Equalf(t, cfg.CliConfig.App.BinDir, filepath.Join(envPath, binPath),
		"wrong bin path")
	assert.Equalf(t, cfg.CliConfig.App.DataDir, filepath.Join(varPath, dataPath),
		"wrong data path")
	assert.Equalf(t, cfg.CliConfig.Modules.Directory, filepath.Join(envPath, modulesPath),
		"wrong modules path")
}

func TestCreatePackageStructure(t *testing.T) {
	testDir := t.TempDir()
	prepareDefaultPackagePaths(testDir)
	err := createPackageStructure(testDir)
	require.NoErrorf(t, err, "failed to create package structure: %v", err)

	expectedToExist := []string{
		varPath,
		varLogPath,
		varRunPath,
		varDataPath,

		envPath,
		envBinPath,
		envModulesPath,
	}

	for _, path := range expectedToExist {
		require.DirExistsf(t, filepath.Join(testDir, path), "the path not found: %s", path)
	}
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
		err = copyAppSrc(filepath.Join(testDir, name), testCopyDir)
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

func TestCopyArtifacts(t *testing.T) {
	testDir := t.TempDir()
	testPackageDir := t.TempDir()

	testOpts := &config.CliOpts{
		App: &config.AppOpts{
			DataDir: filepath.Join(testDir, varDataPath),
			LogDir:  filepath.Join(testDir, varLogPath),
			RunDir:  filepath.Join(testDir, varRunPath),
		},
	}

	var (
		appName      = "app1"
		dataArtifact = "test_file.data"
		logArtifact  = "test_file.log"
	)

	dirsToCreate := []string{
		filepath.Join(varDataPath, appName),
		filepath.Join(varRunPath, appName),
		filepath.Join(varLogPath, appName),
	}
	filesToCreate := []string{
		filepath.Join(varDataPath, appName, dataArtifact),
		filepath.Join(varLogPath, appName, logArtifact),
	}

	packageDirsToCreate := []string{
		filepath.Join(varDataPath, appName),
		filepath.Join(varRunPath, appName),
		filepath.Join(varLogPath, appName),
	}
	err := test_helpers.CreateDirs(testPackageDir, packageDirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	err = test_helpers.CreateDirs(testDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	prepareDefaultPackagePaths(testPackageDir)
	err = copyArtifacts(testOpts, appName)
	require.NoErrorf(t, err, "failed to copy artifacts: %v", err)

	require.FileExists(t, filepath.Join(testPackageDir, varDataPath, appName, dataArtifact))
	require.FileExists(t, filepath.Join(testPackageDir, varLogPath, appName, logArtifact))
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
		instancesEnabledPath,
	}

	err := test_helpers.CreateDirs(testPackageDir, dirsToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateFiles(testPackageDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateFiles(testDir, filesToCreate)
	require.NoErrorf(t, err, "failed to create test directories: %v", err)
	err = test_helpers.CreateSymlink(filepath.Join(testDir, srcApp), appName+".lua")
	require.NoErrorf(t, err, "failed to create test directories: %v", err)

	prepareDefaultPackagePaths(testPackageDir)
	err = createAppSymlink(filepath.Join(testDir, srcApp), appName)
	require.NoErrorf(t, err, "failed to create a symlink: %v", err)

	_, err = os.Lstat(filepath.Join(testPackageDir, instancesEnabledPath, appName))
	require.NoErrorf(t, err, "failed to find a symlink: %v", err)
	resolvedPath, err := filepath.EvalSymlinks(filepath.Join(testPackageDir,
		instancesEnabledPath, appName))
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
