package cluster

import "errors"

// Data represents collected data with its source.
type Data struct {
	// Source is the origin of data, i.e. key in case of etcd or tarantool-based collectors.
	Source string
	// Value is data collected.
	Value []byte
	// Revision is data revision.
	Revision int64
}

// DataCollector interface must be implemented by a source collector.
type DataCollector interface {
	// Collect collects data from a source.
	Collect() ([]Data, error)
}

// DataPublisher interface must be implemented by a raw data publisher.
type DataPublisher interface {
	// Publish publishes the data or returns an error.
	Publish(revision int64, data []byte) error
}

var errDataMissing = errors.New("data does not exist")
