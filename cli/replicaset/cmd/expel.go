package replicasetcmd

import (
	"fmt"

	"github.com/apex/log"

	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

// ExpelCtx contains information about replicaset expel command execution
// context.
type ExpelCtx struct {
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Instance is a target instance name.
	Instance string
	// Orchestrator is a forced orchestator choice.
	Orchestrator replicaset.Orchestrator
}

// Expel expels an instance from a replicaset.
func Expel(expelCtx ExpelCtx) error {
	orchestratorType, err := getOrchestratorApplication(expelCtx.Orchestrator,
		expelCtx.RunningCtx)
	if err != nil {
		return err
	}

	var orchestrator replicasetOrchestrator
	switch orchestratorType {
	case replicaset.OrchestratorCartridge:
		orchestrator = replicaset.NewCartridgeApplication(expelCtx.RunningCtx, nil)
	case replicaset.OrchestratorCentralizedConfig:
		orchestrator = replicaset.NewCConfigApplication(expelCtx.RunningCtx)
	case replicaset.OrchestratorCustom:
		orchestrator = replicaset.NewCustomApplication(expelCtx.RunningCtx)
	default:
		return fmt.Errorf("unsupported orchestrator: %s", orchestratorType)
	}

	log.Info("Discovery application...")
	fmt.Println("")

	// Get and print status.
	replicasets, err := orchestrator.Discovery()
	if err != nil {
		return err
	}
	statusReplicasets(replicasets)

	fmt.Println("")
	log.Infof("Expel instance: %s", expelCtx.Instance)

	// Try to expel the instance.
	err = orchestrator.Expel(expelCtx.Instance)
	if err == nil {
		log.Info("Done.")
	}
	return err
}
