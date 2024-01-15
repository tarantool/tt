package replicasetcmd

import (
	"fmt"

	"github.com/apex/log"

	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

// ExpelCtx contains information about replicaset expel command execution
// context.
type ExpelCtx struct {
	// Instance is a target instance name.
	Instance string
	// Publishers is data publisher factory.
	Publishers libcluster.DataPublisherFactory
	// Collectors is data collector factory.
	Collectors libcluster.DataCollectorFactory
	// Orchestrator is a forced orchestator choice.
	Orchestrator replicaset.Orchestrator
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Force true if unfound instances can be skipped.
	Force bool
	// Timeout describes a timeout in seconds.
	// We keep int as it can be passed to the target instance.
	Timeout int
}

// Expel expels an instance from a replicaset.
func Expel(expelCtx ExpelCtx) error {
	orchestratorType, err := getApplicationOrchestrator(expelCtx.Orchestrator,
		expelCtx.RunningCtx)
	if err != nil {
		return err
	}

	orchestrator, err := makeApplicationOrchestrator(orchestratorType,
		expelCtx.RunningCtx, expelCtx.Collectors, expelCtx.Publishers)
	if err != nil {
		return err
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
	err = orchestrator.Expel(replicaset.ExpelCtx{
		InstName: expelCtx.Instance,
		Force:    expelCtx.Force,
		Timeout:  expelCtx.Timeout,
	})
	if err == nil {
		log.Info("Done.")
	}
	return err
}
