package replicasetcmd

import (
	"fmt"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

const (
	// DefaultTimeout is a default timeout for all operations in seconds.
	DefaultTimeout = 3
)

// replicasetOrchestrator combines replicaset interfaces into a single type.
type replicasetOrchestrator interface {
	replicaset.Discoverer
	replicaset.Promoter
	replicaset.Demoter
	replicaset.Expeller
	replicaset.VShardBootstrapper
	replicaset.Bootstrapper
	replicaset.RolesAdder
}

// makeApplicationOrchestrator creates an orchestrator for the application.
func makeApplicationOrchestrator(
	orchestratorType replicaset.Orchestrator,
	runningCtx running.RunningCtx,
	collectors libcluster.DataCollectorFactory,
	publishers libcluster.DataPublisherFactory) (replicasetOrchestrator, error) {
	var (
		orchestrator replicasetOrchestrator
		err          error
	)
	switch orchestratorType {
	case replicaset.OrchestratorCentralizedConfig:
		orchestrator = replicaset.NewCConfigApplication(runningCtx, collectors, publishers)
	case replicaset.OrchestratorCartridge:
		orchestrator = replicaset.NewCartridgeApplication(runningCtx)
	case replicaset.OrchestratorCustom:
		orchestrator = replicaset.NewCustomApplication(runningCtx)
	default:
		err = fmt.Errorf("unsupported orchestrator: %s", orchestratorType)
	}
	return orchestrator, err
}

// makeInstanceOrchestrator creates an orchestrator for the single instance.
func makeInstanceOrchestrator(orchestratorType replicaset.Orchestrator,
	conn connector.Connector) (replicasetOrchestrator, error) {
	var (
		orchestrator replicasetOrchestrator
		err          error
	)
	switch orchestratorType {
	case replicaset.OrchestratorCentralizedConfig:
		orchestrator = replicaset.NewCConfigInstance(conn)
	case replicaset.OrchestratorCartridge:
		orchestrator = replicaset.NewCartridgeInstance(conn)
	case replicaset.OrchestratorCustom:
		orchestrator = replicaset.NewCustomInstance(conn)
	default:
		err = fmt.Errorf("unsupported orchestrator: %s", orchestratorType)
	}
	return orchestrator, err
}

// getInstanceOrchestrator determines a used orchestrator type for an instance.
func getInstanceOrchestrator(manual replicaset.Orchestrator,
	conn connector.Connector) (replicaset.Orchestrator, error) {
	if manual != replicaset.OrchestratorUnknown {
		return manual, nil
	}

	return replicaset.EvalOrchestrator(conn)
}

// getApplicationOrchestrator determines a used orchestrator type for an application.
func getApplicationOrchestrator(manual replicaset.Orchestrator,
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

// getOrchestratorType determines a used orchestrator.
func getOrchestratorType(
	orchestrator replicaset.Orchestrator,
	conn connector.Connector,
	runningCtx running.RunningCtx) (replicaset.Orchestrator, error) {
	if conn != nil {
		return getInstanceOrchestrator(orchestrator, conn)
	}
	return getApplicationOrchestrator(orchestrator, runningCtx)
}
