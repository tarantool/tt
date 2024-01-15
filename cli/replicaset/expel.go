package replicaset

import "fmt"

// ExpelCtx describes a context for an instance expelling.
type ExpelCtx struct {
	// InstName is an instance name to expel.
	InstName string
	// Force is true when expelling can skip
	// some non-critical checks.
	Force bool
}

// Expeller is an interface for expelling instances from a replicaset.
type Expeller interface {
	// Expel expels instance from a replicasets by its name.
	Expel(ctx ExpelCtx) error
}

// newErrExpelByInstanceNotSupported creates a new error that expel is not
// supported by the orchestrator for a single instance.
func newErrExpelByInstanceNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("expel is not supported for a single instance by %q orchestrator",
		orchestrator)
}

// newErrExpelByAppNotSupported creates a new error that expel by URI is not
// supported by the orchestrator for an application.
func newErrExpelByAppNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("expel is not supported for an application by %q orchestrator",
		orchestrator)
}
