package init

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchellh/mapstructure"
	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
)

func TestLoadCartridgeConfig(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig(&InitCtx{}, "./testdata/valid_cartridge.yml")
	require.NoError(t, err)
	require.Equal(t, configData{
		runDir:             "my_run_dir",
		logDir:             "my_log_dir",
		walDir:             "my_data_dir",
		vinylDir:           "my_data_dir",
		memtxDir:           "my_data_dir",
		tarantoolctlLayout: false,
	}, actualDirInfo)
}

func TestLoadCartridgeInvalidConfig(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig(&InitCtx{}, "./testdata/invalid_cartridge.yml")
	require.EqualError(t, err, "failed to parse cartridge app configuration: failed "+
		"to parse YAML: yaml: line 5: could not find expected ':'")
	require.Equal(t, configData{}, actualDirInfo)
}

func TestLoadCartridgeWrongDataFormat(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig(&InitCtx{}, "./testdata/wrong_data_format.yml")
	require.Contains(t, err.Error(), "'log-dir' expected type 'string', got unconvertible "+
		"type 'float64', value: '1.2'")
	require.Equal(t, configData{}, actualDirInfo)
}

func TestLoadCartridgeNonExistentConfig(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig(&InitCtx{}, "./testdata/no_cartridge.yml")
	require.Error(t, err)
	require.Equal(t, configData{}, actualDirInfo)
}

func checkDefaultEnv(t *testing.T, configName string, instancesEnabled string) {
	rawConfigOpts, err := util.ParseYAML(configName)
	require.NoError(t, err)

	var cfg config.CliOpts
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	assert.Equal(t, instancesEnabled, cfg.Env.InstancesEnabled)
	assert.Equal(t, "var/lib", cfg.App.WalDir)
	assert.Equal(t, "var/lib", cfg.App.VinylDir)
	assert.Equal(t, "var/lib", cfg.App.MemtxDir)
	assert.Equal(t, "var/run", cfg.App.RunDir)
	assert.Equal(t, "var/log", cfg.App.LogDir)
	assert.Equal(t, "bin", cfg.Env.BinDir)
	assert.Equal(t, "modules", cfg.Modules.Directory)
	assert.Equal(t, "distfiles", cfg.Repo.Install)
	assert.Equal(t, "include", cfg.Env.IncludeDir)
	assert.Equal(t, "templates", cfg.Templates[0].Path)
	assert.False(t, cfg.Env.TarantoolctlLayout)

	assert.DirExists(t, instancesEnabled)
	assert.DirExists(t, "modules")
	assert.DirExists(t, "include")
	assert.DirExists(t, "bin")
	assert.DirExists(t, "distfiles")
	assert.DirExists(t, "templates")
}

func TestGenerateTtEnvDefault(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer os.Chdir(wd)

	err = generateTtEnv(configure.ConfigName, configData{
		instancesEnabled: configure.InstancesEnabledDirName,
	})
	require.NoError(t, err)
	require.FileExists(t, configure.ConfigName)
	checkDefaultEnv(t, configure.ConfigName, configure.InstancesEnabledDirName)
}

func TestGenerateTtEnv(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer os.Chdir(wd)

	configPath := filepath.Join(tmpDir, configure.ConfigName)
	err = generateTtEnv(configPath, configData{
		runDir: "run_dir",
		walDir: "wal_dir",
		logDir: "log_dir",
	})
	require.NoError(t, err)
	require.FileExists(t, configPath)

	rawConfigOpts, err := util.ParseYAML(configPath)
	require.NoError(t, err)

	var cfg config.CliOpts
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	// Instances enabled directory must be "." if there is an app in current directory.
	assert.Equal(t, ".", cfg.Env.InstancesEnabled)
	assert.Equal(t, "wal_dir", cfg.App.WalDir)
	assert.Equal(t, "var/lib", cfg.App.VinylDir)
	assert.Equal(t, "var/lib", cfg.App.MemtxDir)
	assert.Equal(t, "run_dir", cfg.App.RunDir)
	assert.Equal(t, "log_dir", cfg.App.LogDir)
	assert.False(t, cfg.Env.TarantoolctlLayout)
	assert.NoDirExists(t, configure.InstancesEnabledDirName)
}

func TestInitRunCartridgeApp(t *testing.T) {
	tmpDir := t.TempDir()
	copy.Copy(filepath.Join("testdata", "valid_cartridge.yml"),
		filepath.Join(tmpDir, ".cartridge.yml"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	f, err := os.Create("init.lua") // Simulate application existence.
	require.NoError(t, err)
	f.Close()

	require.NoError(t, Run(&InitCtx{}))

	rawConfigOpts, err := util.ParseYAML(configure.ConfigName)
	require.NoError(t, err)

	var cfg config.CliOpts
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	assert.Equal(t, ".", cfg.Env.InstancesEnabled)
	assert.Equal(t, "my_data_dir", cfg.App.WalDir)
	assert.Equal(t, "my_data_dir", cfg.App.VinylDir)
	assert.Equal(t, "my_data_dir", cfg.App.MemtxDir)
	assert.Equal(t, "my_run_dir", cfg.App.RunDir)
	assert.Equal(t, "my_log_dir", cfg.App.LogDir)
	assert.False(t, cfg.Env.TarantoolctlLayout)
	assert.NoDirExists(t, configure.InstancesEnabledDirName)
	assert.DirExists(t, "modules")
	assert.DirExists(t, "include")
	assert.DirExists(t, "bin")
	assert.DirExists(t, "distfiles")
	assert.DirExists(t, "templates")
}

func TestInitRunTarantoolctlCfg(t *testing.T) {
	tmpDir := t.TempDir()
	copy.Copy(filepath.Join("testdata", "tarantoolctl_no_snap_dir.lua"),
		filepath.Join(tmpDir, ".tarantoolctl"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	tarantoolExecutable, err := exec.LookPath("tarantool")
	require.NoError(t, err)
	require.NoError(t, Run(&InitCtx{TarantoolExecutable: tarantoolExecutable}))

	rawConfigOpts, err := util.ParseYAML(configure.ConfigName)
	require.NoError(t, err)

	var cfg config.CliOpts
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	assert.Equal(t, "./instances", cfg.Env.InstancesEnabled)
	assert.Equal(t, "./lib", cfg.App.WalDir)
	assert.Equal(t, "./lib", cfg.App.VinylDir)
	assert.Equal(t, "var/lib", cfg.App.MemtxDir)
	assert.Equal(t, "./run", cfg.App.RunDir)
	assert.Equal(t, "./log", cfg.App.LogDir)
	assert.True(t, cfg.Env.TarantoolctlLayout)
	assert.DirExists(t, cfg.Env.InstancesEnabled)
	assert.DirExists(t, "modules")
	assert.DirExists(t, "include")
	assert.DirExists(t, "bin")
	assert.DirExists(t, "distfiles")
	assert.DirExists(t, "templates")

	// Simulate application existence.
	f, err := os.Create("init.lua")
	require.NoError(t, err)
	f.Close()

	require.NoError(t, Run(&InitCtx{
		TarantoolExecutable: tarantoolExecutable,
		ForceMode:           true,
	}))

	rawConfigOpts, err = util.ParseYAML(configure.ConfigName)
	require.NoError(t, err)

	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	assert.Equal(t, "./instances", cfg.Env.InstancesEnabled)
	assert.Equal(t, "./lib", cfg.App.WalDir)
	assert.Equal(t, "./lib", cfg.App.VinylDir)
	assert.Equal(t, "var/lib", cfg.App.MemtxDir)
	assert.Equal(t, "./run", cfg.App.RunDir)
	assert.Equal(t, "./log", cfg.App.LogDir)
	assert.True(t, cfg.Env.TarantoolctlLayout)
	assert.DirExists(t, cfg.Env.InstancesEnabled)
	assert.DirExists(t, "modules")
	assert.DirExists(t, "include")
	assert.DirExists(t, "bin")
	assert.DirExists(t, "distfiles")
	assert.DirExists(t, "templates")
}

func TestInitRunInvalidConfigAppDir(t *testing.T) {
	tmpDir := t.TempDir()
	copy.Copy(filepath.Join("testdata", "invalid_cartridge.yml"),
		filepath.Join(tmpDir, ".cartridge.yml"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	f, err := os.Create("init.lua") // Simulate application existence.
	require.NoError(t, err)
	f.Close()

	require.EqualError(t, Run(&InitCtx{}), "failed to parse cartridge app "+
		"configuration: failed to parse YAML: yaml: line 5: could not find expected ':'")
}

func TestInitRunInvalidConfigNoAppDir(t *testing.T) {
	tmpDir := t.TempDir()
	copy.Copy(filepath.Join("testdata", "invalid_cartridge.yml"),
		filepath.Join(tmpDir, ".cartridge.yml"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.EqualError(t, Run(&InitCtx{}), "failed to parse cartridge app "+
		"configuration: failed to parse YAML: yaml: line 5: could not find expected ':'")
}

func TestInitRunNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.NoError(t, Run(&InitCtx{}))
	checkDefaultEnv(t, configure.ConfigName, configure.InstancesEnabledDirName)
}

func TestInitRunFailCreateResultFile(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	_, err = os.Create(configure.ConfigName)
	require.NoError(t, err)
	// Make target file read-only.
	require.NoError(t, os.Chmod(configure.ConfigName, 0400))

	require.Error(t, Run(&InitCtx{}))
}

func TestInitRunInvalidConfigSkipIt(t *testing.T) {
	tmpDir := t.TempDir()
	copy.Copy(filepath.Join("testdata", "invalid_cartridge.yml"),
		filepath.Join(tmpDir, ".cartridge.yml"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.NoError(t, Run(&InitCtx{SkipConfig: true}))
	checkDefaultEnv(t, configure.ConfigName, configure.InstancesEnabledDirName)
}

func TestCreateDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.NoError(t, createDirectories([]string{
		"dir1",
		"dir2",
		"",
		"dir3/subdir",
	}))
	assert.DirExists(t, "dir1")
	assert.DirExists(t, "dir2")
	assert.DirExists(t, "dir3/subdir")
}

func TestInitRunOverwriteTtEnv(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	f, err := os.Create(configure.ConfigName)
	require.NoError(t, err)
	f.WriteString("text")
	f.Close()

	require.NoError(t, Run(&InitCtx{reader: strings.NewReader("Y\n")}))
	// Make sure the file is overwritten.
	checkDefaultEnv(t, configure.ConfigName, configure.InstancesEnabledDirName)

	// Test overwrite of existing tt.yml file.
	require.NoError(t, os.Remove(configure.ConfigName))
	f, err = os.Create("tt.yml")
	require.NoError(t, err)
	f.WriteString("text")
	f.Close()

	require.NoError(t, Run(&InitCtx{reader: strings.NewReader("Y\n")}))
	// Make sure the file is overwritten.
	checkDefaultEnv(t, "tt.yml", configure.InstancesEnabledDirName)

	// Multiple configs - error.
	require.NoError(t, copy.Copy("tt.yml", configure.ConfigName))
	require.Error(t, Run(&InitCtx{reader: strings.NewReader("\n")}))
}

func TestInitRunDontOverwriteTtEnv(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	f, err := os.Create(configure.ConfigName)
	require.NoError(t, err)
	f.WriteString("text")
	f.Close()

	require.NoError(t, Run(&InitCtx{reader: strings.NewReader("N\n")}))
	// Make sure the file has old data.
	require.NoError(t, err)
	buf, err := os.ReadFile(configure.ConfigName)
	require.NoError(t, err)
	require.Equal(t, "text", string(buf))

	// Test the same but with .yml file.
	err = os.Rename(configure.ConfigName, "tt.yml")
	require.NoError(t, err)

	require.NoError(t, Run(&InitCtx{reader: strings.NewReader("N\n")}))
	// Make sure the file has old data.
	require.NoError(t, err)
	assert.NoFileExists(t, configure.ConfigName)
	buf, err = os.ReadFile("tt.yml")
	require.NoError(t, err)
	require.Equal(t, "text", string(buf))
}

func TestCheckExistingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	f, err := os.Create(configure.ConfigName)
	require.NoError(t, err)
	f.Close()

	fileName, err := checkExistingConfig(&InitCtx{reader: strings.NewReader("y\n")})
	assert.NoError(t, err)
	assert.Equal(t, configure.ConfigName, fileName)

	f, err = os.Create(configure.ConfigName)
	require.NoError(t, err)
	f.Close()
	fileName, err = checkExistingConfig(&InitCtx{reader: strings.NewReader("n\n")})
	assert.NoError(t, err)
	assert.Equal(t, "", fileName)

	fileName, err = checkExistingConfig(&InitCtx{reader: strings.NewReader("n\n"),
		ForceMode: true})
	assert.NoError(t, err)
	assert.Equal(t, configure.ConfigName, fileName)
}

func TestInitLoadTarantoolctlConfig(t *testing.T) {
	var err error
	var initCtx InitCtx
	initCtx.TarantoolExecutable, err = exec.LookPath("tarantool")
	require.NoError(t, err)
	cfg, err := loadTarantoolctlConfig(&initCtx, "testdata/tarantoolctl_workdir.lua")
	require.NoError(t, err)
	assert.Equal(t, "", cfg.walDir)
	assert.Equal(t, "", cfg.vinylDir)
	assert.Equal(t, "", cfg.memtxDir)
	assert.Equal(t, "", cfg.logDir)
	assert.Equal(t, "", cfg.runDir)

	os.Setenv("TEST_WORKDIR", "/tmp/workdir")
	cfg, err = loadTarantoolctlConfig(&initCtx, "testdata/tarantoolctl_workdir.lua")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/workdir", cfg.walDir)
	assert.Equal(t, "/tmp/workdir", cfg.vinylDir)
	assert.Equal(t, "/tmp/workdir", cfg.memtxDir)
	assert.Equal(t, "/tmp/workdir", cfg.logDir)
	assert.Equal(t, "/tmp/workdir", cfg.runDir)

	os.Unsetenv("TEST_WORKDIR")
	cfg, err = loadTarantoolctlConfig(&initCtx, "testdata/tarantoolctl.lua")
	require.NoError(t, err)
	assert.Equal(t, "var/lib", cfg.walDir)
	assert.Equal(t, "var/lib", cfg.vinylDir)
	assert.Equal(t, "var/snap", cfg.memtxDir)
	assert.Equal(t, "var/log", cfg.logDir)
	assert.Equal(t, "var/run", cfg.runDir)

	cfg, err = loadTarantoolctlConfig(&initCtx, "testdata/tarantoolctl_no_snap_dir.lua")
	require.NoError(t, err)
	assert.Equal(t, "./lib", cfg.walDir)
	assert.Equal(t, "./lib", cfg.vinylDir)
	assert.Equal(t, "", cfg.memtxDir)
	assert.Equal(t, "./log", cfg.logDir)
	assert.Equal(t, "./run", cfg.runDir)
	assert.Equal(t, "./instances", cfg.instancesEnabled)

	cfg, err = loadTarantoolctlConfig(&initCtx,
		"testdata/tarantoolctl_different_directories.lua")
	require.NoError(t, err)
	assert.Equal(t, "var/wal", cfg.walDir)
	assert.Equal(t, "var/vinyl", cfg.vinylDir)
	assert.Equal(t, "var/snap", cfg.memtxDir)
	assert.Equal(t, "var/log", cfg.logDir)
	assert.Equal(t, "var/pid", cfg.runDir)
	assert.Equal(t, "", cfg.instancesEnabled)
	assert.True(t, cfg.tarantoolctlLayout)
}

func TestInitLoadTarantoolctlConfigErrorCases(t *testing.T) {
	var err error
	var initCtx InitCtx
	initCtx.TarantoolExecutable, err = exec.LookPath("tarantool")
	require.NoError(t, err)

	_, err = loadTarantoolctlConfig(&initCtx,
		"testdata/tarantoolctl_invalid.lua")
	require.True(t, strings.Contains(err.Error(),
		"tarantoolctl config loading error: LuajitError:"))
}
