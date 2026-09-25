package replicasetcmd

import (
	"fmt"
	"os"

	"github.com/tarantool/tt/sdk/log"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/replicaset"
	"github.com/tarantool/tt/v3/cli/running"
)

// ExpelCtx contains information about replicaset expel command execution
// context.
type ExpelCtx struct {
	// Instance is a target instance name.
	Instance string
	// Publishers is data publisher factory.
	Publishers sdkcluster.Factory
	// Collectors is data collector factory.
	Collectors sdkcluster.Factory
	// Integrity is the integrity context for cluster reads.
	Integrity integrity.IntegrityCtx
	// Orchestrator is a forced orchestrator choice.
	Orchestrator replicaset.Orchestrator
	// RunningCtx is an application running context.
	RunningCtx running.RunningCtx
	// Force true if unavailable instances can be skipped.
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
		expelCtx.RunningCtx, expelCtx.Collectors, expelCtx.Publishers, expelCtx.Integrity)
	if err != nil {
		return err
	}

	log.Info("Discovery application...")

	_, _ = fmt.Fprintln(os.Stdout, "")

	// Get and print status.
	replicasets, err := orchestrator.Discovery(replicaset.SkipCache)
	if err != nil {
		return err
	}

	_ = statusReplicasets(replicasets)

	_, _ = fmt.Fprintln(os.Stdout, "")

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
