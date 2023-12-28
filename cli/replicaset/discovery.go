package replicaset

import (
	"fmt"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
)

// DiscoveryApplication retrieves replicasets information for instances in an
// application. If orchestrator == OrchestratorUnknown then it tries to
// determinate an orchestrator.
func DiscoveryApplication(app running.RunningCtx,
	orchestrator Orchestrator) (Replicasets, error) {
	if orchestrator == OrchestratorUnknown {
		eval := func(_ running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			var err error
			orchestrator, err = EvalOrchestrator(evaler)
			return true, err
		}

		if err := EvalAny(app.Instances, InstanceEvalFunc(eval)); err != nil {
			return Replicasets{}, fmt.Errorf("unable to determinate orchestrator: %w", err)
		}
	}

	switch orchestrator {
	case OrchestratorCartridge:
		return NewCartridgeApplication(app, nil).GetReplicasets()
	case OrchestratorCentralizedConfig:
		return NewCConfigApplication(app).GetReplicasets()
	case OrchestratorCustom:
		return NewCustomApplication(app).GetReplicasets()
	default:
		return Replicasets{}, fmt.Errorf("orchestrator is not supported: %s", orchestrator)
	}
}

// DiscoveryInstance retrieves replicasets information from a connection.
// If orchestrator == OrchestratorUnknown then it tries to determinate an
// orchestrator.
func DiscoveryInstance(evaler connector.Evaler,
	orchestrator Orchestrator) (Replicasets, error) {
	if orchestrator == OrchestratorUnknown {
		var err error
		orchestrator, err = EvalOrchestrator(evaler)
		if err != nil {
			return Replicasets{}, fmt.Errorf("unable to determinate orchestrator: %w", err)
		}
	}

	switch orchestrator {
	case OrchestratorCartridge:
		return NewCartridgeInstance(evaler).GetReplicasets()
	case OrchestratorCentralizedConfig:
		return NewCConfigInstance(evaler).GetReplicasets()
	case OrchestratorCustom:
		return NewCustomInstance(evaler).GetReplicasets()
	default:
		return Replicasets{}, fmt.Errorf("orchestartor is not supported: %s", orchestrator)
	}
}
