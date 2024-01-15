package replicaset

import (
	"fmt"
)

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

// Expeller is an insterface for expelling instances from a replicaset.
type Expeller interface {
	// Expel expels instance from a replicasets by its name.
	Expel(name string) error
}
