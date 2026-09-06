package replicasetcmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/integrity"
)

var (
	errTheReplicasetCanNotBeSpecifiedInTheCaseOfApplicationBootstrapping = errors.New(
		"the replicaset can not be specified in the case of application bootstrapping",
	)
	errTheReplicasetMustBeSpecifiedToBootstrapAnInstance = errors.New(
		"the replicaset must be specified to bootstrap an instance",
	)
)

// BootstrapCtx describes context to bootstrap an instance or application.
type BootstrapCtx struct {
	// Orchestrator is a forced orchestrator choice.
	Orchestrator replicaset.Orchestrator
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
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

// Bootstrap bootstraps an instance or application.
func Bootstrap(ctx BootstrapCtx) error {
	if ctx.Instance != "" {
		if ctx.Replicaset == "" {
			return errTheReplicasetMustBeSpecifiedToBootstrapAnInstance
		}
	} else {
		if ctx.Replicaset != "" {
			return errTheReplicasetCanNotBeSpecifiedInTheCaseOfApplicationBootstrapping
		}
	}

	orchestratorType, err := getApplicationOrchestrator(ctx.Orchestrator,
		ctx.RunningCtx)
	if err != nil {
		return err
	}

	orchestrator, err := makeApplicationOrchestrator(orchestratorType,
		ctx.RunningCtx, libcluster.Factory{}, libcluster.Factory{}, integrity.IntegrityCtx{})
	if err != nil {
		return err
	}

	err = orchestrator.Bootstrap(replicaset.BootstrapCtx{
		Timeout:         ctx.Timeout,
		Instance:        ctx.Instance,
		Replicaset:      ctx.Replicaset,
		BootstrapVShard: ctx.BootstrapVShard,
	})
	if err == nil {
		// Re-discovery and log topology.
		replicasets, err := orchestrator.Discovery(replicaset.SkipCache)
		if err != nil {
			return err
		}
		statusReplicasets(replicasets)
		fmt.Fprintln(os.Stdout)
		log.Info("Done.")
	}
	return err
}
