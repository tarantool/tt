package cluster

import (
	"fmt"

	"github.com/tarantool/tt/cli/integrity"
	"gopkg.in/yaml.v3"
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

// Collect collects configuration from YAML data.
func (collector YamlCollector) Collect() (*Config, error) {
	config := NewConfig()
	if err := yaml.Unmarshal(collector.data, config); err != nil {
		return nil, fmt.Errorf("unable to unmarshal YAML: %w", err)
	}

	return config, nil
}

// YamlCollectorDecorator is a wrapper over integrity.DataCollector
// implementing Collector.
type YamlCollectorDecorator struct {
	rawCollector integrity.DataCollector
}

// Collect collects configuration from raw data interpretening it
// as YAML.
func (collector YamlCollectorDecorator) Collect() (*Config, error) {
	raw, err := collector.rawCollector.Collect()
	if err != nil {
		return nil, err
	}

	cconfig := NewConfig()

	for _, data := range raw {
		if config, err := NewYamlCollector(data.Value).Collect(); err != nil {
			return nil, fmt.Errorf("failed to decode config from %q: %w", data.Source, err)
		} else {
			cconfig.Merge(config)
		}
	}

	return cconfig, nil
}

// NewYamlCollectorDecorator wraps a DataCollector to interpret raw data as
// YAML configurations.
func NewYamlCollectorDecorator(collector integrity.DataCollector) YamlCollectorDecorator {
	return YamlCollectorDecorator{
		rawCollector: collector,
	}
}

// YamlConfigPublisher publishes a configuration as YAML via the base
// publisher.
type YamlConfigPublisher struct {
	// publisher used to publish the YAML data.
	publisher integrity.DataPublisher
}

// NewYamlConfigPublisher creates a new YamlConfigPublisher object to publish
// a configuration via the publisher.
func NewYamlConfigPublisher(publisher integrity.DataPublisher) YamlConfigPublisher {
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
