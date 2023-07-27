package cluster

// Collector interface must be implemented by a configuration source collector.
type Collector interface {
	// Collect collects a configuration or returns an error.
	Collect() (*Config, error)
}
