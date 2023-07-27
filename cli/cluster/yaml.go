package cluster

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

// YamlCollector collects a configuration from YAML data.
type YamlCollector struct {
	// data is a raw YAML data.
	data []byte
}

// NewYamlCollector create a new YAML collector.
func NewYamlCollector(data []byte) YamlCollector {
	return YamlCollector{
		data: data,
	}
}

// Collect collects a configuration from YAML data.
func (c YamlCollector) Collect() (*Config, error) {
	config := NewConfig()
	if err := yaml.Unmarshal(c.data, config); err != nil {
		return nil, fmt.Errorf("unable to unmarshal YAML: %w", err)
	}

	return config, nil
}
