package replicaset

// PromoteCtx describes a context for an instance promoting.
type PromoteCtx struct {
	// InstName is an instance name to promote.
	InstName string
	// Force is true when promoting can skip
	// some non-critical checks.
	Force bool
	// Timeout is a timeout for promoting waitings in seconds.
	// Keep int, because it can be passed to the target instance.
	Timeout int
}

// Promoter is an interface to promote an instance in the replicaset.
type Promoter interface {
	// Promote promotes an instance.
	Promote(PromoteCtx) error
}
