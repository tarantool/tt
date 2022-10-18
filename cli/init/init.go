package init

import (
	"fmt"
	"os"
	"regexp"

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

	// Create instances enabled directory if required.
	if appDirInfo.instancesEnabled != "" {
		cfg.CliConfig.App.InstancesEnabled = appDirInfo.instancesEnabled
		if err := util.CreateDirectory(appDirInfo.instancesEnabled,
			defaultDirPermissions); err != nil {
			return err
		}
		log.Debugf("'%s' directory is created.", appDirInfo.instancesEnabled)
	}

	// Create modules directory.
	if cfg.CliConfig.Modules.Directory != "" {
		if err := util.CreateDirectory(cfg.CliConfig.Modules.Directory,
			defaultDirPermissions); err != nil {
			return err
		}
		log.Debugf("'%s' directory is created.", cfg.CliConfig.Modules.Directory)
	}

	// Create include directory.
	if cfg.CliConfig.App.IncludeDir != "" {
		if err := util.CreateDirectory(cfg.CliConfig.App.IncludeDir,
			defaultDirPermissions); err != nil {
			return err
		}
		log.Debugf("'%s' directory is created.", cfg.CliConfig.App.IncludeDir)
	}

	// Create binary directory.
	if cfg.CliConfig.App.BinDir != "" {
		if err := util.CreateDirectory(cfg.CliConfig.App.BinDir,
			defaultDirPermissions); err != nil {
			return err
		}
		log.Debugf("'%s' directory is created.", cfg.CliConfig.App.BinDir)
	}

	// Create install directory.
	if cfg.CliConfig.App.BinDir != "" {
		if err := util.CreateDirectory(cfg.CliConfig.Repo.Install,
			defaultDirPermissions); err != nil {
			return err
		}
		log.Debugf("'%s' directory is created.", cfg.CliConfig.Repo.Install)
	}

	// Create templates directories.
	if cfg.CliConfig.App.BinDir != "" {
		for _, templatesPathOpts := range cfg.CliConfig.Templates {
			if err := util.CreateDirectory(templatesPathOpts.Path,
				defaultDirPermissions); err != nil {
				return err
			}
			log.Debugf("'%s' directory is created.", templatesPathOpts)
		}
	}

	return nil
}

// Run creates tt environment config for the application in current dir.
func Run(initCtx *InitCtx) error {
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
	if !util.IsApp(".", []*regexp.Regexp{}) {
		// Current directory is not app dir, so set default instances enabled dir.
		appDirInfo.instancesEnabled = configure.InstancesEnabledDirName
	}

	if err := generateTtEnv(configure.ConfigName, appDirInfo); err != nil {
		return err
	}

	log.Infof("Environment config is written to '%s'", configure.ConfigName)

	return nil
}
