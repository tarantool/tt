package replicasetcmd

import (
	"errors"
	"fmt"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/replicaset"
	"github.com/tarantool/tt/v3/cli/running"
)

var (
	errUnsupportedOrchestrator = errors.New("unsupported orchestrator: ")
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
	replicaset.RolesChanger
}

// makeApplicationOrchestrator creates an orchestrator for the application.
func makeApplicationOrchestrator(
	orchestratorType replicaset.Orchestrator,
	runningCtx running.RunningCtx,
	collectors sdkcluster.Factory,
	publishers sdkcluster.Factory,
	integ integrity.IntegrityCtx,
) (replicasetOrchestrator, error) {
	var (
		orchestrator replicasetOrchestrator
		err          error
	)

	switch orchestratorType {
	case replicaset.OrchestratorCentralizedConfig:
		orchestrator = replicaset.NewCConfigApplication(runningCtx, collectors, publishers, integ)
	case replicaset.OrchestratorCustom:
		orchestrator = replicaset.NewCustomApplication(runningCtx)
	default:
		err = fmt.Errorf("%w%s", errUnsupportedOrchestrator, orchestratorType)
	}

	return orchestrator, err
}

// makeInstanceOrchestrator creates an orchestrator for the single instance.
func makeInstanceOrchestrator(orchestratorType replicaset.Orchestrator,
	conn connector.Connector,
) (replicasetOrchestrator, error) {
	var (
		orchestrator replicasetOrchestrator
		err          error
	)

	switch orchestratorType {
	case replicaset.OrchestratorCentralizedConfig:
		orchestrator = replicaset.NewCConfigInstance(conn)
	case replicaset.OrchestratorCustom:
		orchestrator = replicaset.NewCustomInstance(conn)
	default:
		err = fmt.Errorf("%w%s", errUnsupportedOrchestrator, orchestratorType)
	}

	return orchestrator, err
}

// DiscoverInstance reads the live topology through the instance's orchestrator.
// Backup planning uses the same discovery implementation as replicaset commands.
func DiscoverInstance(conn connector.Connector) (replicaset.Replicasets, error) {
	orchestratorType, err := getInstanceOrchestrator(replicaset.OrchestratorUnknown, conn)
	if err != nil {
		return replicaset.Replicasets{}, fmt.Errorf("failed to determine orchestrator: %w", err)
	}

	orchestrator, err := makeInstanceOrchestrator(orchestratorType, conn)
	if err != nil {
		return replicaset.Replicasets{}, fmt.Errorf("failed to create orchestrator: %w", err)
	}

	topology, err := orchestrator.Discovery(replicaset.SkipCache)
	if err != nil {
		return replicaset.Replicasets{}, fmt.Errorf("failed to discover topology: %w", err)
	}

	return topology, nil
}

// getInstanceOrchestrator determines a used orchestrator type for an instance.
func getInstanceOrchestrator(manual replicaset.Orchestrator,
	conn connector.Connector,
) (replicaset.Orchestrator, error) {
	if manual != replicaset.OrchestratorUnknown {
		return manual, nil
	}

	return replicaset.EvalOrchestrator(conn)
}

// getApplicationOrchestrator determines a used orchestrator type for an application.
func getApplicationOrchestrator(manual replicaset.Orchestrator,
	runningCtx running.RunningCtx,
) (replicaset.Orchestrator, error) {
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

	err := replicaset.EvalAny(instances, replicaset.InstanceEvalFunc(eval))
	if err != nil {
		return orchestrator,
			fmt.Errorf("unable to determinate an orchestrator type: %w", err)
	}

	return orchestrator, nil
}

// getOrchestratorType determines a used orchestrator.
func getOrchestratorType(
	orchestrator replicaset.Orchestrator,
	conn connector.Connector,
	runningCtx running.RunningCtx,
) (replicaset.Orchestrator, error) {
	if conn != nil {
		return getInstanceOrchestrator(orchestrator, conn)
	}

	return getApplicationOrchestrator(orchestrator, runningCtx)
}
