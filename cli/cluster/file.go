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
func (c FileCollector) Collect() (*Config, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("unable to read a file %q: %w", c.path, err)
	}

	config, err := NewYamlCollector(data).Collect()
	if err != nil {
		return nil, fmt.Errorf("unable to parse a file %q: %w", c.path, err)
	}
	return config, nil
}
