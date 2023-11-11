package replicaset

// State defines an enumeration of available replicaset states.
type State int

//go:generate stringer -type=State -trimprefix State -linecomment

const (
	// StateUnknown when is unable to get a replicaset configuration.
	StateUnknown State = iota // unknown
	// StateUninitialized when a configuration found, but not bootstrapped yet.
	StateUninitialized // uninitialized
	// StateBootstrapped when a replicaset already bootstrapped.
	StateBootstrapped // bootstrapped
)
