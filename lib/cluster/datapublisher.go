package cluster

// DataPublisher interface must be implemented by a raw data publisher.
type DataPublisher interface {
	// Publish publishes the data or returns an error.
	Publish(revision int64, data []byte) error
}
