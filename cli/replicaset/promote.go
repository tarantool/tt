package replicaset

import "fmt"

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

// newErrPromoteByInstanceNotSupported creates a new error that promote is not
// supported by the orchestrator for a single instance.
func newErrPromoteByInstanceNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("promote is not supported for a single instance by %q orchestrator",
		orchestrator)
}

// newErrPromoteByAppNotSupported creates a new error that promote is not
// supported by the orchestrator for an application.
func newErrPromoteByAppNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("promote is not supported for an application by %q orchestrator",
		orchestrator)
}
