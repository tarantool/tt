package cluster

import (
	"fmt"
	"io"
	"os"
)

type FileReadFunc func(path string) (io.ReadCloser, error)

// FileCollector collects data from a YAML file.
type FileCollector struct {
	path         string
	fileReadFunc FileReadFunc
}

// NewFileCollector creates a new file collector for a path.
func NewFileCollector(path string) FileCollector {
	return FileCollector{path: path, fileReadFunc: func(p string) (io.ReadCloser, error) {
		return os.Open(p)
	}}
}

// Collect collects a configuration from a file located at a specified path.
func (collector FileCollector) Collect() ([]Data, error) {
	const fmtErr = "unable to read file %q: %w"

	reader, err := collector.fileReadFunc(collector.path)
	if err != nil {
		return nil, fmt.Errorf(fmtErr, collector.path, err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf(fmtErr, collector.path, err)
	}
	return []Data{{Source: collector.path, Value: data}}, nil
}

// FilePublisher publishes data into a file as is.
type FilePublisher struct {
	path string
}

// NewFilePublisher creates a new FilePublisher that writes published data
// into the file at the given path.
func NewFilePublisher(path string) FilePublisher {
	return FilePublisher{path: path}
}

// Publish publishes the data to a file for the given path.
func (publisher FilePublisher) Publish(revision int64, data []byte) error {
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

	if err := os.WriteFile(publisher.path, data, 0o644); err != nil {
		return fmt.Errorf("failed to publish data into %q: %w", publisher.path, err)
	}
	return nil
}
