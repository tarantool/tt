package replicaset

// DemoteCtx describes a context for an instance demoting.
type DemoteCtx struct {
	// InstName is an instance name to demote.
	InstName string
	// Force is true when demoting can skip
	// some non-critical checks.
	Force bool
	// Timeout is a timeout for demoting waitings in seconds.
	// Keep int, because it can be passed to the target instance.
	Timeout int
}

// Demoter is an interface to demote an instance in the replicaset.
type Demoter interface {
	// Demote demotes an instance.
	Demote(DemoteCtx) error
}
