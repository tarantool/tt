package cluster

import (
	"fmt"
	"os"
)

// EnvCollector collects a configuration from environment variables.
type EnvCollector struct {
	// formatter is a helper function that converts a path to a target
	// environment variable.
	formatter func(path []string) string
}

// NewEnvCollector creates a new EnvCollector. A path to an environment
// variable format function must be specified.
func NewEnvCollector(formatter func(path []string) string) EnvCollector {
	return EnvCollector{
		formatter: formatter,
	}
}

// Collect collects a configuration from environment variables.
func (collector EnvCollector) Collect() (*Config, error) {
	config := NewConfig()

	for _, p := range ConfigEnvPaths {
		env := collector.formatter(p)
		if value, ok := os.LookupEnv(env); ok {
			if err := config.Set(p, value); err != nil {
				fmtErr := "unable to create a config from ENV: %w"
				return nil, fmt.Errorf(fmtErr, err)
			}
		}
	}

	return config, nil
}
