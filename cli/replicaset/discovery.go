package replicaset

// Discoverer is an interface for discovering information about
// replicasets.
type Discoverer interface {
	// Discovery returns replicasets information or an error.
	Discovery() (Replicasets, error)
}
