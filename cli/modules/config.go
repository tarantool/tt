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
type CliOpts struct {
	Modules *struct {
		Directory string
	}
}

// GetCliOpts returns Tarantool CLI options from the config file
// located at path configurePath.
func GetCliOpts(configurePath string) (*CliOpts, error) {
	// Config could not be processed - we ignore it and
	// continue working without config.
	if _, err := os.Stat(configurePath); err != nil {
		// TODO: Add warning in next patches, discussion
		// what if the file exists, but access is denied, etc.
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to get access to configuration file: %s", err)
		}

		return nil, nil
	}

	rawConfigOpts, err := util.ParseYAML(configurePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	var cfg Config
	if err := mapstructure.Decode(rawConfigOpts, &cfg); err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	if cfg.CliConfig == nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: missing tt section")
	}

	return cfg.CliConfig, nil
}
