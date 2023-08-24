package cluster

// DataPublisher interface must be implemented by a raw data publisher.
type DataPublisher interface {
	// Publish publishes the data or returns an error.
	Publish(data []byte) error
}

// ConfigPublisher interface must be implemented by a config publisher.
type ConfigPublisher interface {
	// Publish publisher the configuration or returns an error.
	Publish(config *Config) error
}
