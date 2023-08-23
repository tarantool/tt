package cluster

import (
	"fmt"
	"os"
)

// FileCollector collects data from a YAML file.
type FileCollector struct {
	// path is a path to a YAML file.
	path string
}

// NewFileCollector create a new file collector for a path.
func NewFileCollector(path string) FileCollector {
	return FileCollector{
		path: path,
	}
}

// Collect collects a configuration from a file located at a specified path.
func (collector FileCollector) Collect() (*Config, error) {
	data, err := os.ReadFile(collector.path)
	if err != nil {
		return nil, fmt.Errorf("unable to read a file %q: %w",
			collector.path, err)
	}

	config, err := NewYamlCollector(data).Collect()
	if err != nil {
		return nil, fmt.Errorf("unable to parse a file %q: %w",
			collector.path, err)
	}
	return config, nil
}
