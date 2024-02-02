package cluster

import (
	"fmt"
	"os"

	"github.com/tarantool/tt/cli/integrity"
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
func (collector FileCollector) Collect() ([]integrity.Data, error) {
	data, err := os.ReadFile(collector.path)
	if err != nil {
		return nil, fmt.Errorf("unable to read a file %q: %w",
			collector.path, err)
	}

	if err != nil {
		return nil, fmt.Errorf("unable to parse a file %q: %w",
			collector.path, err)
	}
	return []integrity.Data{
		{
			Source: collector.path,
			Value:  data,
		},
	}, nil
}

// FileDataPublisher publishes a data into a file as is.
type FileDataPublisher struct {
	// path is a path to the file.
	path string
}

// NewFileDataPublisher creates a new FileDataPublisher object to publish
// a data into a file for the given path.
func NewFileDataPublisher(path string) FileDataPublisher {
	return FileDataPublisher{
		path: path,
	}
}

// Publish publishes the data to a file for the given path.
func (publisher FileDataPublisher) Publish(revision int64, data []byte) error {
	if revision != 0 {
		return fmt.Errorf("failed to publish data into file: target revision %d is not supported",
			revision)
	}
	if publisher.path == "" {
		return fmt.Errorf("file path is empty")
	}
	if data == nil {
		return fmt.Errorf("failed to publish data into %q: data does not exist",
			publisher.path)
	}

	err := os.WriteFile(publisher.path, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to publish data into %q: %w",
			publisher.path, err)
	}
	return nil
}
