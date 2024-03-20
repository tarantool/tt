package replicasetcmd

import (
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
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
}

// getOrchestratorInstance determinates a used orchestrator type for an instance.
func getOrchestratorInstance(manual replicaset.Orchestrator,
	conn connector.Connector) (replicaset.Orchestrator, error) {
	if manual != replicaset.OrchestratorUnknown {
		return manual, nil
	}

	return replicaset.EvalOrchestrator(conn)
}
