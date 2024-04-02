package replicaset

import "fmt"

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

// newErrDemoteByInstanceNotSupported creates a new error that demote is not
// supported by the orchestrator for a single instance.
func newErrDemoteByInstanceNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("demote is not supported for a single instance by %q orchestrator",
		orchestrator)
}

// newErrDemoteByAppNotSupported creates a new error that demote is not
// supported by the orchestrator for an application.
func newErrDemoteByAppNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("demote is not supported for an application by %q orchestrator",
		orchestrator)
}
