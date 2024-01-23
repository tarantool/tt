package replicasetcmd

import (
	"fmt"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

// replicasetOrchestrator combines replicaset interfaces into a single type.
type replicasetOrchestrator interface {
	replicaset.Discoverer
	replicaset.Expeller
}

// getOrchestratorInstance determinates a used orchestrator type for an instance.
func getOrchestratorInstance(manual replicaset.Orchestrator,
	conn connector.Connector) (replicaset.Orchestrator, error) {
	if manual != replicaset.OrchestratorUnknown {
		return manual, nil
	}

	return replicaset.EvalOrchestrator(conn)
}

// getOrchestratorApplication determinates a used orchestrator type for an application.
func getOrchestratorApplication(manual replicaset.Orchestrator,
	runningCtx running.RunningCtx) (replicaset.Orchestrator, error) {
	if manual != replicaset.OrchestratorUnknown {
		return manual, nil
	}

	var orchestrator replicaset.Orchestrator
	eval := func(_ running.InstanceCtx, evaler connector.Evaler) (bool, error) {
		instanceOrchestrator, err := replicaset.EvalOrchestrator(evaler)
		if err == nil {
			orchestrator = instanceOrchestrator
		}
		return true, err
	}

	instances := runningCtx.Instances
	if err := replicaset.EvalAny(instances, replicaset.InstanceEvalFunc(eval)); err != nil {
		return orchestrator,
			fmt.Errorf("unable to determinate an orchestrator type: %w", err)
	}
	return orchestrator, nil
}
