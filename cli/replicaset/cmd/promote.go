package replicasetcmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"

	libcluster "github.com/tarantool/tt/lib/cluster"
)

// PromoteCtx describes the context to promote an instance.
type PromoteCtx struct {
	// InstName is an instance name to promote.
	InstName string
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
	// IsApplication true if an application passed.
	IsApplication bool
	// Orchestrator is a forced orchestator choice.
	Orchestrator replicaset.Orchestrator
	// Conn is an active connection to a passed instance.
	Conn connector.Connector
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Force true if unfound instances can be skipped.
	Force bool
	// Timeout describes a timeout in seconds.
	// We keep int as it can be passed to the target instance.
	Timeout int
}

// Promote promotes an instance.
func Promote(ctx PromoteCtx) error {
	orchestratorType, err := getInstanceOrchestrator(ctx.Orchestrator, ctx.Conn)
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
		if orchestrator, err = makeInstanceOrchestrator(orchestratorType, ctx.Conn); err != nil {
			return err
		}
	}

	log.Info("Discovery application...")
	fmt.Println()

	// Get and print status.
	replicasets, err := orchestrator.Discovery(true)
	if err != nil {
		return err
	}
	statusReplicasets(replicasets)
	fmt.Println()

	if ctx.InstName != "" {
		log.Infof("Promote instance: %s", ctx.InstName)
	}

	err = orchestrator.Promote(replicaset.PromoteCtx{
		InstName: ctx.InstName,
		Force:    ctx.Force,
		Timeout:  ctx.Timeout,
	})
	if err == nil {
		log.Info("Done.")
	}
	return err
}
