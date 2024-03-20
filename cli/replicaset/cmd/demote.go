package replicasetcmd

import (
	"fmt"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

// DemoteCtx describes the context to demote an instance.
type DemoteCtx struct {
	// InstName is an instance name to demote.
	InstName string
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
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

// Demote demotes an instance.
func Demote(ctx DemoteCtx) error {
	orchestratorType, err := getOrchestratorInstance(ctx.Orchestrator, ctx.Conn)
	if err != nil {
		return err
	}

	var orchestrator interface {
		replicaset.Discoverer
		replicaset.Demoter
	}
	switch orchestratorType {
	case replicaset.OrchestratorCentralizedConfig:
		orchestrator = replicaset.NewCConfigApplication(ctx.RunningCtx, ctx.Collectors,
			ctx.Publishers)
	case replicaset.OrchestratorCartridge:
		fallthrough
	case replicaset.OrchestratorCustom:
		fallthrough
	default:
		return fmt.Errorf("unsupported orchestrator: %s", orchestratorType)
	}

	log.Info("Discovery application...")
	fmt.Println()

	// Get and print status.
	replicasets, err := orchestrator.Discovery()
	if err != nil {
		return err
	}
	statusReplicasets(replicasets)
	fmt.Println()

	if ctx.InstName != "" {
		log.Infof("Demote instance: %s", ctx.InstName)
	}

	err = orchestrator.Demote(replicaset.DemoteCtx{
		InstName: ctx.InstName,
		Force:    ctx.Force,
		Timeout:  ctx.Timeout,
	})
	if err == nil {
		log.Info("Done.")
	}
	return err
}
