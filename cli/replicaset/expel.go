package replicaset

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/v3/cli/running"
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
	// some non-critical checks: that the instance is no longer connected to
	// the replicaset, and that the other members can be reached.
	Force bool
	// Timeout is a timeout for expelling waitings in seconds.
	// Keep int, because it can be passed to the target instance.
	Timeout int
	// Instances are the instances tt can reach, for an expel from a config
	// storage (CConfigSource): those of them that are members of the
	// replicaset of the instance tell whether it still runs, reload the
	// config and delete it from _cluster. An application has its own.
	Instances []running.InstanceCtx
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
