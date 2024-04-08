package replicasetcmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

const (
	// VShardBootstrapDefaultTimeout is a default timeout for vshard bootstrapping.
	VShardBootstrapDefaultTimeout = 10
)

// VShardCmdCtx describes context for vshard commands.
type VShardCmdCtx struct {
	// IsApplication true if an application passed.
	IsApplication bool
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Conn is an active connection to a passed instance.
	Conn connector.Connector
	// Orchestrator is a forced orchestator choice.
	Orchestrator replicaset.Orchestrator
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
	// Timeout describes a timeout in seconds.
	// We keep int as it can be passed to the target instance.
	Timeout int
}

// BootstrapVShard bootstraps vshard in the cluster.
func BootstrapVShard(ctx VShardCmdCtx) error {
	orchestratorType, err := getOrchestratorType(ctx.Orchestrator, ctx.Conn, ctx.RunningCtx)
	if err != nil {
		return err
	}

	var orchestrator replicasetOrchestrator
	if ctx.IsApplication {
		if orchestrator, err = makeApplicationOrchestrator(
			orchestratorType, ctx.RunningCtx, ctx.Collectors, ctx.Publishers); err != nil {
			return err
		}
	} else {
		if orchestrator, err = makeInstanceOrchestrator(
			orchestratorType, ctx.Conn); err != nil {
			return err
		}
	}

	log.Info("Discovery application...")
	fmt.Println("")

	// Get and print status.
	replicasets, err := orchestrator.Discovery(true)
	if err != nil {
		return err
	}
	statusReplicasets(replicasets)

	fmt.Println("")
	log.Info("Bootstrapping vshard")

	err = orchestrator.BootstrapVShard(replicaset.VShardBootstrapCtx{Timeout: ctx.Timeout})
	if err == nil {
		log.Info("Done.")
	}

	return err
}
