package modules

import (
	"fmt"
	"os"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/util"
)

// Config used to store all information from the
// tarantool.yaml configuration file.
type Config struct {
	CliConfig *CliOpts `mapstructure:"tt"`
}

// CliOpts stores information about Tarantool CLI configuration.
// Filled in when parsing the tarantool.yaml configuration file.
//
// tarantool.yaml file format:
// tt:
//   modules:
//     directory: path/to
//...app:
//     available: path
//     restart_on_failure: bool

type modulesOpts struct {
	Directory string
}

type appOpts struct {
	InstancesAvailable string `mapstructure:"instances_available"`
	Restartable        bool   `mapstructure:"restart_on_failure"`
}

type CliOpts struct {
	Modules *modulesOpts
	App     *appOpts
}

// getDefaultCliOpts returns `CliOpts`filled with default values.
func getDefaultCliOpts() *CliOpts {
	modules := modulesOpts{
		Directory: "",
	}
	app := appOpts{
		InstancesAvailable: "",
		Restartable:        false,
	}
	return &CliOpts{Modules: &modules, App: &app}
}

// GetCliOpts returns Tarantool CLI options from the config file
// located at path configurePath.
func GetCliOpts(configurePath string) (*CliOpts, error) {
	var cfg Config
	// Config could not be processed.
	if _, err := os.Stat(configurePath); err != nil {
		// TODO: Add warning in next patches, discussion
		// what if the file exists, but access is denied, etc.
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to get access to configuration file: %s", err)
		}

		cfg.CliConfig = getDefaultCliOpts()
		return cfg.CliConfig, nil
	}

	rawConfigOpts, err := util.ParseYAML(configurePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	if err := mapstructure.Decode(rawConfigOpts, &cfg); err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	if cfg.CliConfig == nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: missing tt section")
	}

	return cfg.CliConfig, nil
}
