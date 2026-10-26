package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/cli/replicaset"
)

func TestOrchestrator_String(t *testing.T) {
	cases := []struct {
		Orchestrator replicaset.Orchestrator
		Expected     string
	}{
		{replicaset.OrchestratorUnknown, "unknown"},
		{replicaset.OrchestratorCentralizedConfig, "centralized config"},
		{replicaset.OrchestratorCustom, "custom"},
		{replicaset.Orchestrator(123), "Orchestrator(123)"},
	}

	for _, testCase := range cases {
		t.Run(testCase.Expected, func(t *testing.T) {
			assert.Equal(t, testCase.Expected, testCase.Orchestrator.String())
		})
	}
}
