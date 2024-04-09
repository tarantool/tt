package replicaset

import "fmt"

// VShardBootstrapCtx describes context to bootstrap vshard.
type VShardBootstrapCtx struct {
	// Timeout is a timeout for bootstrapping in seconds.
	// Keep int, because it can be passed to the target instance.
	Timeout int
}

// VShardBootstrapper performs vshard bootstrapping.
type VShardBootstrapper interface {
	// BootstrapVShard bootstraps vshard in the cluster.
	BootstrapVShard(ctx VShardBootstrapCtx) error
}

// newErrBootstrapVShardByInstanceNotSupported creates a new error that vshard bootstrap is not
// supported by the orchestrator for a single instance.
func newErrBootstrapVShardByInstanceNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("bootstrap vshard is not supported for a single instance by %q orchestrator",
		orchestrator)
}

// newErrBootstrapVShardByAppNotSupported creates a new error that vshard bootstrap is not
// supported by the orhestrator for an application.
func newErrBootstrapVShardByAppNotSupported(orchestrator Orchestrator) error {
	return fmt.Errorf("bootstrap vshard is not supported for an application by %q orchestrator",
		orchestrator)
}
