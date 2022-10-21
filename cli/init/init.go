package init

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v2"
)

type cartridgeOpts struct {
	LogDir  string `mapstructure:"log-dir"`
	RunDir  string `mapstructure:"run-dir"`
	DataDir string `mapstructure:"data-dir"`
}

// appDirInfo contains directories config info loaded from existing config.
type appDirInfo struct {
	logDir  string
	runDir  string
	dataDir string
}

// configLoader binds config name with load functor.
type configLoader struct {
	configName string
	load       func(string) (appDirInfo, error)
}

// loadCartridgeConfig parses configPath as .cartridge.yml and fill directories info structure.
func loadCartridgeConfig(configPath string) (appDirInfo, error) {
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

// generateTtEnvConfig generates environment config in configPath using directories info from
// appDirInfo.
func generateTtEnvConfig(configPath string, appDirInfo appDirInfo) error {
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

	file, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warnf("Failed to close a file '%s': %s", file.Name(), err)
		}
	}()

	if err = yaml.NewEncoder(file).Encode(&cfg); err != nil {
		return err
	}

	return nil
}

// Run creates tt environment config for the application in current dir.
func Run(initCtx *cmdcontext.InitCtx) error {
	configLoaders := []configLoader{
		{".cartridge.yml", loadCartridgeConfig},
	}

	var appDirInfo appDirInfo
	var err error
	if !initCtx.SkipConfig {
		for _, confLoader := range configLoaders {
			if _, err = os.Stat(confLoader.configName); err != nil {
				if os.IsNotExist(err) {
					continue
				} else {
					log.Warnf("Failed to get info of '%s': %s", confLoader.configName, err)
				}
			}

			appDirInfo, err = confLoader.load(confLoader.configName)
			if err != nil {
				return err
			}
		}
	}

	if err := generateTtEnvConfig(configure.ConfigName, appDirInfo); err != nil {
		return err
	}

	log.Infof("Environment config is written to '%s'", configure.ConfigName)

	return nil
}
