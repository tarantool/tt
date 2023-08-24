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
func (collector YamlCollector) Collect() (*Config, error) {
	config := NewConfig()
	if err := yaml.Unmarshal(collector.data, config); err != nil {
		return nil, fmt.Errorf("unable to unmarshal YAML: %w", err)
	}

	return config, nil
}

// YamlConfigPublisher publishes a configuration as YAML via the base
// publisher.
type YamlConfigPublisher struct {
	// publisher used to publish the YAML data.
	publisher DataPublisher
}

// NewYamlConfigPublisher creates a new YamlConfigPublisher object to publish
// a configuration via the publisher.
func NewYamlConfigPublisher(publisher DataPublisher) YamlConfigPublisher {
	return YamlConfigPublisher{
		publisher: publisher,
	}
}

// Publish publishes the configuration as YAML data.
func (publisher YamlConfigPublisher) Publish(config *Config) error {
	if config == nil {
		return fmt.Errorf("config does not exist")
	}

	return publisher.publisher.Publish([]byte(config.String()))
}
