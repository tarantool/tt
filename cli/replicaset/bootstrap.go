package replicaset

import "fmt"

// BootstrapCtx describes the context to bootstrap an instance/application.
type BootstrapCtx struct {
	// ReplicasetsFile is a Cartridge replicasets file.
	ReplicasetsFile string
	// Timeout describes a timeout in seconds.
	// We keep int as it can be passed to the target instance.
	Timeout int
	// BootstrapVShard is true when the vshard must be bootstrapped.
	BootstrapVShard bool
	// Instance is an instance name to bootstrap.
	Instance string
	// Replicaset is a replicaset name for an instance bootstrapping.
	Replicaset string
}

// Bootstrapper is an interface to bootstrap an instance/application.
type Bootstrapper interface {
	// Bootstrap bootstraps an instance/application.
	Bootstrap(ctx BootstrapCtx) error
}

// newErrBootstrapByInstanceNotSupported creates a new error that bootstrap is not
// supported by the orchestrator for a single instance.
func newErrBootstrapByInstanceNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("bootstrap is not supported for a single instance by %q orchestrator",
		orchestrator)
}

// newErrBootstrapByAppNotSupported creates a new error that bootstrap is not
// supported by the orchestrator for an application.
func newErrBootstrapByAppNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("bootstrap is not supported for an application by %q orchestrator",
		orchestrator)
}
