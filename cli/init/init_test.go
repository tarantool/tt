package init

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchellh/mapstructure"
	"github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
)

func TestLoadCartridgeConfig(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig("./testdata/valid_cartridge.yml")
	require.NoError(t, err)
	require.Equal(t, appDirInfo{
		runDir:  "my_run_dir",
		logDir:  "my_log_dir",
		dataDir: "my_data_dir",
	}, actualDirInfo)
}

func TestLoadCartridgeInvalidConfig(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig("./testdata/invalid_cartridge.yml")
	require.EqualError(t, err, "failed to parse cartridge app configuration: Failed "+
		"to parse YAML: yaml: line 5: could not find expected ':'")
	require.Equal(t, appDirInfo{}, actualDirInfo)
}

func TestLoadCartridgeWrongDataFormat(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig("./testdata/wrong_data_format.yml")
	require.Contains(t, err.Error(), "'log-dir' expected type 'string', got unconvertible "+
		"type 'float64', value: '1.2'")
	require.Equal(t, appDirInfo{}, actualDirInfo)
}

func TestLoadCartridgeNonExistentConfig(t *testing.T) {
	actualDirInfo, err := loadCartridgeConfig("./testdata/no_cartridge.yml")
	require.Error(t, err)
	require.Equal(t, appDirInfo{}, actualDirInfo)
}

func TestGenerateTtEnvConfigDefault(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, configure.ConfigName)
	err := generateTtEnvConfig(configPath, appDirInfo{})
	require.NoError(t, err)
	require.FileExists(t, configPath)

	rawConfigOpts, err := util.ParseYAML(configPath)
	require.NoError(t, err)

	var cfg config.Config
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	require.Equal(t, ".", cfg.CliConfig.App.InstancesEnabled)
	require.Equal(t, "var/lib", cfg.CliConfig.App.DataDir)
	require.Equal(t, "var/run", cfg.CliConfig.App.RunDir)
	require.Equal(t, "var/log", cfg.CliConfig.App.LogDir)
	require.Equal(t, 10, cfg.CliConfig.App.LogMaxBackups)
	require.Equal(t, 100, cfg.CliConfig.App.LogMaxSize)
	require.Equal(t, 8, cfg.CliConfig.App.LogMaxAge)
	require.Equal(t, "bin", cfg.CliConfig.App.BinDir)
	require.Equal(t, "modules", cfg.CliConfig.Modules.Directory)
	require.Equal(t, "install", cfg.CliConfig.Repo.Install)
	require.Equal(t, "include", cfg.CliConfig.App.IncludeDir)
	require.Equal(t, "templates", cfg.CliConfig.Templates[0].Path)
}

func TestGenerateTtEnvConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, configure.ConfigName)
	err := generateTtEnvConfig(configPath, appDirInfo{
		runDir:  "run_dir",
		dataDir: "data_dir",
		logDir:  "log_dir",
	})
	require.NoError(t, err)
	require.FileExists(t, configPath)

	rawConfigOpts, err := util.ParseYAML(configPath)
	require.NoError(t, err)

	var cfg config.Config
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	require.Equal(t, ".", cfg.CliConfig.App.InstancesEnabled)
	require.Equal(t, "data_dir", cfg.CliConfig.App.DataDir)
	require.Equal(t, "run_dir", cfg.CliConfig.App.RunDir)
	require.Equal(t, "log_dir", cfg.CliConfig.App.LogDir)
}

func TestInitRun(t *testing.T) {
	tmpDir := t.TempDir()
	copy.Copy(filepath.Join("testdata", "valid_cartridge.yml"),
		filepath.Join(tmpDir, ".cartridge.yml"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.NoError(t, Run(&cmdcontext.InitCtx{}))

	rawConfigOpts, err := util.ParseYAML(configure.ConfigName)
	require.NoError(t, err)

	var cfg config.Config
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	require.Equal(t, ".", cfg.CliConfig.App.InstancesEnabled)
	require.Equal(t, "my_data_dir", cfg.CliConfig.App.DataDir)
	require.Equal(t, "my_run_dir", cfg.CliConfig.App.RunDir)
	require.Equal(t, "my_log_dir", cfg.CliConfig.App.LogDir)
}

func TestInitRunInvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	copy.Copy(filepath.Join("testdata", "invalid_cartridge.yml"),
		filepath.Join(tmpDir, ".cartridge.yml"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.EqualError(t, Run(&cmdcontext.InitCtx{}), "failed to parse cartridge app "+
		"configuration: Failed to parse YAML: yaml: line 5: could not find expected ':'")
}

func TestInitRunNoConfig(t *testing.T) {
	tmpDir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.NoError(t, Run(&cmdcontext.InitCtx{}))

	rawConfigOpts, err := util.ParseYAML(configure.ConfigName)
	require.NoError(t, err)

	var cfg config.Config
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	require.Equal(t, ".", cfg.CliConfig.App.InstancesEnabled)
	require.Equal(t, "var/lib", cfg.CliConfig.App.DataDir)
	require.Equal(t, "var/run", cfg.CliConfig.App.RunDir)
	require.Equal(t, "var/log", cfg.CliConfig.App.LogDir)
	require.Equal(t, 10, cfg.CliConfig.App.LogMaxBackups)
	require.Equal(t, 100, cfg.CliConfig.App.LogMaxSize)
	require.Equal(t, 8, cfg.CliConfig.App.LogMaxAge)
	require.Equal(t, "bin", cfg.CliConfig.App.BinDir)
	require.Equal(t, "modules", cfg.CliConfig.Modules.Directory)
	require.Equal(t, "install", cfg.CliConfig.Repo.Install)
	require.Equal(t, "include", cfg.CliConfig.App.IncludeDir)
	require.Equal(t, "templates", cfg.CliConfig.Templates[0].Path)
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

	require.Error(t, Run(&cmdcontext.InitCtx{}))
}

func TestInitRunInvalidConfigSkipIt(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, configure.ConfigName)
	copy.Copy(filepath.Join("testdata", "invalid_cartridge.yml"),
		filepath.Join(tmpDir, ".cartridge.yml"))
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer os.Chdir(wd)

	require.NoError(t, Run(&cmdcontext.InitCtx{SkipConfig: true}))

	rawConfigOpts, err := util.ParseYAML(configPath)
	require.NoError(t, err)

	var cfg config.Config
	require.NoError(t, mapstructure.Decode(rawConfigOpts, &cfg))

	require.Equal(t, ".", cfg.CliConfig.App.InstancesEnabled)
	require.Equal(t, "var/lib", cfg.CliConfig.App.DataDir)
	require.Equal(t, "var/run", cfg.CliConfig.App.RunDir)
	require.Equal(t, "var/log", cfg.CliConfig.App.LogDir)
	require.Equal(t, 10, cfg.CliConfig.App.LogMaxBackups)
	require.Equal(t, 100, cfg.CliConfig.App.LogMaxSize)
	require.Equal(t, 8, cfg.CliConfig.App.LogMaxAge)
	require.Equal(t, "bin", cfg.CliConfig.App.BinDir)
}
