package cluster

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
