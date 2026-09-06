package replicaset

import (
	"errors"
	"fmt"
)

var (
	errExpelIsNotSupported = errors.New(
		"expel is not supported for ",
	)
)

// ExpelCtx describes a context for an instance expelling.
type ExpelCtx struct {
	// InstName is an instance name to expel.
	InstName string
	// Force is true when expelling can skip
	// some non-critical checks.
	Force bool
	// Timeout is a timeout for expelling waitings in seconds.
	// Keep int, because it can be passed to the target instance.
	Timeout int
}

// Expeller is an interface for expelling instances from a replicaset.
type Expeller interface {
	// Expel expels instance from a replicasets by its name.
	Expel(ctx ExpelCtx) error
}

// newErrExpelByInstanceNotSupported creates a new error that expel is not
// supported by the orchestrator for a single instance.
func newErrExpelByInstanceNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("%wa single instance by %q orchestrator",
		errExpelIsNotSupported, orchestrator)
}

// newErrExpelByAppNotSupported creates a new error that expel by URI is not
// supported by the orchestrator for an application.
func newErrExpelByAppNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("%wan application by %q orchestrator",
		errExpelIsNotSupported, orchestrator)
}
