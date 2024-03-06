package cluster

import (
	"fmt"

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

// YamlDataMergeCollector collects and merges configuration
// from the slice of Data.
type YamlDataMergeCollector struct {
	data []Data
}

// NewYamlDataMergeCollector creates YamlDataMergeCollector.
func NewYamlDataMergeCollector(data ...Data) YamlDataMergeCollector {
	return YamlDataMergeCollector{
		data: data,
	}
}

// Collect collects configuration from YAML data.
func (collector YamlDataMergeCollector) Collect() (*Config, error) {
	cconfig := NewConfig()

	for _, src := range collector.data {
		if config, err := NewYamlCollector(src.Value).Collect(); err != nil {
			return nil, fmt.Errorf("failed to decode config from %q: %w", src.Source, err)
		} else {
			cconfig.Merge(config)
		}
	}

	return cconfig, nil
}

// YamlCollectorDecorator is a wrapper over DataCollector
// implementing Collector.
type YamlCollectorDecorator struct {
	rawCollector DataCollector
}

// Collect collects configuration from raw data interpretening it
// as YAML.
func (collector YamlCollectorDecorator) Collect() (*Config, error) {
	raw, err := collector.rawCollector.Collect()
	if err != nil {
		return nil, err
	}
	return NewYamlDataMergeCollector(raw...).Collect()
}

// NewYamlCollectorDecorator wraps a DataCollector to interpret raw data as
// YAML configurations.
func NewYamlCollectorDecorator(collector DataCollector) YamlCollectorDecorator {
	return YamlCollectorDecorator{
		rawCollector: collector,
	}
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

	return publisher.publisher.Publish(0, []byte(config.String()))
}
