package replicaset_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/replicaset"
)

var (
	errFoo = errors.New("foo")
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

func TestParseOrchestrator(t *testing.T) {
	cases := []struct {
		String   string
		Expected replicaset.Orchestrator
	}{
		{"foo", replicaset.OrchestratorUnknown},
		{"unknown", replicaset.OrchestratorUnknown},
		{"centralized config", replicaset.OrchestratorCentralizedConfig},
		{"CentRALIZED CONFIG", replicaset.OrchestratorCentralizedConfig},
		{"custom", replicaset.OrchestratorCustom},
		{"CUSTOM", replicaset.OrchestratorCustom},
	}

	for _, testCase := range cases {
		t.Run(testCase.String, func(t *testing.T) {
			parsed := replicaset.ParseOrchestrator(testCase.String)
			require.Equal(t, testCase.Expected, parsed)
		})
	}
}

type orchestratorEvalerMock struct {
	ret []any
	err error
}

func (m orchestratorEvalerMock) Eval(expr string,
	args []any, opts connector.RequestOpts,
) ([]any, error) {
	return m.ret, m.err
}

func TestEvalOrchestrator(t *testing.T) {
	cases := []struct {
		Expected replicaset.Orchestrator
		Ret      []any
	}{
		{replicaset.OrchestratorCentralizedConfig, []any{"centralized config"}},
		{replicaset.OrchestratorCustom, []any{"custom"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.Expected.String(), func(t *testing.T) {
			orchestrator, err := replicaset.EvalOrchestrator(orchestratorEvalerMock{
				ret: testCase.Ret,
			})
			require.NoError(t, err)
			assert.Equal(t, testCase.Expected, orchestrator)
		})
	}
}

func TestEvalOrchestrator_invalid_response(t *testing.T) {
	cases := [][]any{
		nil,
		{},
		{1},
		{"unknown", 2},
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("%v", testCase), func(t *testing.T) {
			_, err := replicaset.EvalOrchestrator(orchestratorEvalerMock{
				ret: testCase,
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
		ret: []any{"unknown"},
		err: errFoo,
	})
	require.EqualError(t, err, "failed to recognize orchestrator: foo")
}
