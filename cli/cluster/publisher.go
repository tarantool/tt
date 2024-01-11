package cluster

// ConfigPublisher interface must be implemented by a config publisher.
type ConfigPublisher interface {
	// Publish publisher the configuration or returns an error.
	Publish(config *Config) error
}
