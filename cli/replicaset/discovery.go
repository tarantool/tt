package replicaset

import (
	"fmt"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
)

// Discoverer is an interface for discovering information about
// replicasets.
type Discoverer interface {
	// Discovery returns replicasets information or an error.
	Discovery() (Replicasets, error)
}

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
		return NewCartridgeApplication(app).Discovery()
	case OrchestratorCentralizedConfig:
		return NewCConfigApplication(app, nil, nil).Discovery()
	case OrchestratorCustom:
		return NewCustomApplication(app).Discovery()
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
		return NewCartridgeInstance(evaler).Discovery()
	case OrchestratorCentralizedConfig:
		return NewCConfigInstance(evaler).Discovery()
	case OrchestratorCustom:
		return NewCustomInstance(evaler).Discovery()
	default:
		return Replicasets{}, fmt.Errorf("orchestartor is not supported: %s", orchestrator)
	}
}
