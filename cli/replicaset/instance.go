package replicaset

// Instance describes an instance in a replicaset.
type Instance struct {
	// Alias is a human-readable instance name.
	Alias string
	// UUID of the instance.
	UUID string
	// URI of the instance.
	URI string
	// Mode of the instance.
	Mode Mode
}
