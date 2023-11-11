package replicaset_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
)

func TestOrchestrator_String(t *testing.T) {
	cases := []struct {
		Orchestrator replicaset.Orchestrator
		Expected     string
	}{
		{replicaset.OrchestratorUnknown, "unknown"},
		{replicaset.OrchestratorCartridge, "cartridge"},
		{replicaset.OrchestratorCentralizedConfig, "centralized config"},
		{replicaset.OrchestratorCustom, "custom"},
		{replicaset.Orchestrator(123), "Orchestrator(123)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Orchestrator.String())
		})
	}
}

func TestParseOrchestrator(t *testing.T) {
	cases := []struct {
		String   string
		Expected replicaset.Orchestrator
	}{
		{"foo", replicaset.OrchestratorUnknown},
		{"unknown", replicaset.OrchestratorUnknown},
		{"cartridge", replicaset.OrchestratorCartridge},
		{"carTridge", replicaset.OrchestratorCartridge},
		{"centralized config", replicaset.OrchestratorCentralizedConfig},
		{"CentRALIZED CONFIG", replicaset.OrchestratorCentralizedConfig},
		{"custom", replicaset.OrchestratorCustom},
		{"CUSTOM", replicaset.OrchestratorCustom},
	}

	for _, tc := range cases {
		t.Run(tc.String, func(t *testing.T) {
			parsed := replicaset.ParseOrchestrator(tc.String)
			require.Equal(t, tc.Expected, parsed)
		})
	}
}

type orchestratorEvalerMock struct {
	ret []any
	err error
}

func (m orchestratorEvalerMock) Eval(expr string,
	args []any, opts connector.RequestOpts) ([]any, error) {
	return m.ret, m.err
}

func TestEvalOrchestrator(t *testing.T) {
	cases := []struct {
		Expected replicaset.Orchestrator
		Ret      []any
	}{
		{replicaset.OrchestratorCartridge, []any{"cartridge"}},
		{replicaset.OrchestratorCentralizedConfig, []any{"centralized config"}},
		{replicaset.OrchestratorCustom, []any{"custom"}},
	}
	for _, tc := range cases {
		t.Run(tc.Expected.String(), func(t *testing.T) {
			orchestrator, err := replicaset.EvalOrchestrator(orchestratorEvalerMock{
				ret: tc.Ret,
			})
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, orchestrator)
		})
	}
}

func TestEvalOrchestrator_invalid_response(t *testing.T) {
	cases := [][]any{
		nil,
		[]any{},
		[]any{1},
		[]any{"cartridge", 2},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%v", tc), func(t *testing.T) {
			_, err := replicaset.EvalOrchestrator(orchestratorEvalerMock{
				ret: tc,
			})
			assert.EqualError(t, err, "unexpected response")
		})
	}
}

func TestEvalOrchestrator_unknown(t *testing.T) {
	_, err := replicaset.EvalOrchestrator(orchestratorEvalerMock{
		ret: []any{"foo"},
	})
	require.EqualError(t, err, "unknown orchestrator: foo")
}

func TestEvalOrchestrator_error(t *testing.T) {
	_, err := replicaset.EvalOrchestrator(orchestratorEvalerMock{
		ret: []any{"cartridge"},
		err: errors.New("foo"),
	})
	require.EqualError(t, err, "failed to recognize orchestrator: foo")
}
