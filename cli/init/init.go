package init

import (
	// Go embed blank import.
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/apex/log"
	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
)

const (
	defaultDirPermissions = os.FileMode(0750)
)

// InitCtx contains information for tt config creation.
type InitCtx struct {
	// SkipConfig - if set, disables cartridge & tarantoolctl config analysis,
	// so init does not try to get directories information from exitsting config files.
	SkipConfig bool
	// ForceMode, if set, tt config is re-written without a question.
	ForceMode bool
	// reader to use for reading user input.
	reader io.Reader
	// Tarantool executable path.
	TarantoolExecutable string
}

type cartridgeOpts struct {
	LogDir  string `mapstructure:"log-dir"`
	RunDir  string `mapstructure:"run-dir"`
	DataDir string `mapstructure:"data-dir"`
}

// appDirInfo contains directories config info loaded from existing config.
type appDirInfo struct {
	instancesEnabled string
	logDir           string
	runDir           string
	dataDir          string
}

// configLoader binds config name with load functor.
type configLoader struct {
	configName string
	load       func(*InitCtx, string) (appDirInfo, error)
}

//go:embed print_tarantoolctl_cfg.lua
var printTarantoolctlCfgLuaBytes []byte

// loadCartridgeConfig parses configPath as .cartridge.yml and fill directories info structure.
func loadCartridgeConfig(initCtx *InitCtx, configPath string) (appDirInfo, error) {
	var cartridgeConf cartridgeOpts
	rawConfigOpts, err := util.ParseYAML(configPath)
	if err != nil {
		return appDirInfo{}, fmt.Errorf("failed to parse cartridge app configuration: %s", err)
	}

	if err := mapstructure.Decode(rawConfigOpts, &cartridgeConf); err != nil {
		return appDirInfo{}, fmt.Errorf("failed to parse cartridge app configuration: %s", err)
	}

	return appDirInfo{
		runDir:  cartridgeConf.RunDir,
		logDir:  cartridgeConf.LogDir,
		dataDir: cartridgeConf.DataDir,
	}, nil
}

// createDirectories creates directories specified in dirList.
func createDirectories(dirList []string) error {
	for _, dirName := range dirList {
		if dirName == "" {
			continue
		}
		if err := util.CreateDirectory(dirName, defaultDirPermissions); err != nil {
			return err
		}
		log.Debugf("'%s' directory is created.", dirName)
	}
	return nil
}

// loadTarantoolctlConfig loads data from configPath which is processed as a lua file, and fills
// directories info structure.
func loadTarantoolctlConfig(initCtx *InitCtx, configPath string) (appDirInfo, error) {
	var appDirInfo appDirInfo
	if initCtx.TarantoolExecutable == "" {
		return appDirInfo, fmt.Errorf("Tarantool executable is not set")
	}

	out, err := util.ExecuteCommandGetOutput(initCtx.TarantoolExecutable, "",
		printTarantoolctlCfgLuaBytes, "-", configPath)
	if err != nil {
		return appDirInfo, fmt.Errorf("tarantoolctl config loading error: %s", string(out))
	}
	outLines := strings.Split(string(out), "\n")

	for _, dirDefinition := range outLines {
		if dirDefinition == "" {
			continue
		}
		varName, dirPath, found := strings.Cut(dirDefinition, "=")
		if !found || varName == "" {
			log.Warnf("Failed to parse output of tarantoolctl : %s", dirDefinition)
		}
		switch varName {
		case "wal_dir":
			appDirInfo.dataDir = dirPath
		case "logger":
			appDirInfo.logDir = dirPath
		case "pid_file":
			appDirInfo.runDir = dirPath
		default:
			log.Warnf("Unknown var: %s", varName)
		}
	}

	return appDirInfo, nil
}

// generateTtEnv generates environment config in configPath using directories info from
// appDirInfo.
func generateTtEnv(configPath string, appDirInfo appDirInfo) error {
	cfg := util.GenerateDefaulTtEnvConfig()
	if appDirInfo.runDir != "" {
		cfg.CliConfig.App.RunDir = appDirInfo.runDir
	}
	if appDirInfo.dataDir != "" {
		cfg.CliConfig.App.DataDir = appDirInfo.dataDir
	}
	if appDirInfo.logDir != "" {
		cfg.CliConfig.App.LogDir = appDirInfo.logDir
	}
	if appDirInfo.instancesEnabled != "" {
		cfg.CliConfig.App.InstancesEnabled = appDirInfo.instancesEnabled
	}

	if err := util.WriteYaml(configPath, cfg); err != nil {
		return err
	}

	if appDirInfo.instancesEnabled != "" {
		cfg.CliConfig.App.InstancesEnabled = appDirInfo.instancesEnabled
	}

	directoriesToCreate := []string{
		cfg.CliConfig.App.InstancesEnabled,
		cfg.CliConfig.Modules.Directory,
		cfg.CliConfig.App.IncludeDir,
		cfg.CliConfig.App.BinDir,
		cfg.CliConfig.Repo.Install,
	}
	for _, templatesPathOpts := range cfg.CliConfig.Templates {
		directoriesToCreate = append(directoriesToCreate, templatesPathOpts.Path)
	}

	return createDirectories(directoriesToCreate)
}

// FillCtx initializes init context.
func FillCtx(initCtx *InitCtx) {
	initCtx.reader = os.Stdin
}

// checkExistingConfig checks tt config for existence and asks for confirmation to overwrite.
// Returns file name if init process can continue, and false otherwise. In case of error, non-nil
// error returned as second returned value.
func checkExistingConfig(initCtx *InitCtx) (string, error) {
	configName, err := util.GetYamlFileName(configure.ConfigName, false)
	if configName == "" {
		return "", err
	}

	if _, err := os.Stat(configName); err == nil {
		if initCtx.ForceMode {
			if err = os.Remove(configName); err != nil {
				return "", err
			}
		} else {
			confirmed, err := util.AskConfirm(initCtx.reader,
				fmt.Sprintf("%s already exists. Overwrite?", configName))
			if err != nil {
				return "", err
			}
			if confirmed {
				if err = os.Remove(configName); err != nil {
					return "", err
				}
			} else {
				log.Info("Init is cancelled by user.")
				return "", nil
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return configName, nil
}

// Run creates tt environment config for the application in current dir.
func Run(initCtx *InitCtx) error {
	if initCtx.reader == nil {
		initCtx.reader = os.Stdin
	}

	configLoaders := []configLoader{
		{".cartridge.yml", loadCartridgeConfig},
		{".tarantoolctl", loadTarantoolctlConfig},
	}

	configName, err := checkExistingConfig(initCtx)
	if configName == "" {
		return err
	}

	var appDirInfo appDirInfo
	if !initCtx.SkipConfig {
		for _, confLoader := range configLoaders {
			if _, err = os.Stat(confLoader.configName); err != nil {
				if os.IsNotExist(err) {
					continue
				} else {
					log.Warnf("Failed to get info of '%s': %s", confLoader.configName, err)
				}
			}

			log.Infof("Found existing config '%s'", confLoader.configName)
			appDirInfo, err = confLoader.load(initCtx, confLoader.configName)
			if err != nil {
				return err
			} else {
				break
			}
		}
	}
	if !util.IsApp(".") {
		// Current directory is not app dir, so set default instances enabled dir.
		appDirInfo.instancesEnabled = configure.InstancesEnabledDirName
	}

	if err := generateTtEnv(configName, appDirInfo); err != nil {
		return err
	}

	log.Infof("Environment config is written to '%s'", configure.ConfigName)

	return nil
}
