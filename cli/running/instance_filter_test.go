package running

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/process_utils"
)

func TestCompletionHelpers(t *testing.T) {
	savedGetStatus := getStatus
	defer func() {
		getStatus = savedGetStatus
	}()

	instances := []InstanceCtx{
		{AppName: "single_run", SingleApp: true},

		{AppName: "app1", InstName: "master"},
		{AppName: "app1", InstName: "replica"},

		{AppName: "single_stopped", SingleApp: true},

		{AppName: "app2", InstName: "master"},
		{AppName: "app2", InstName: "replica1"},
		{AppName: "app2", InstName: "replica2"},

		{AppName: "app3", InstName: "master"},
		{AppName: "app3", InstName: "replica1"},
		{AppName: "app3", InstName: "stateboard"},
	}

	statuses := map[string]process_utils.ProcessState{
		"single_run": process_utils.ProcStateRunning,

		"app1:master":  process_utils.ProcStateRunning,
		"app1:replica": process_utils.ProcStateRunning,

		"single_stopped": process_utils.ProcStateStopped,

		"app2:master":   process_utils.ProcStateDead,
		"app2:replica1": process_utils.ProcStateStopped,
		"app2:replica2": process_utils.ProcStateStopped,

		"app3:master":     process_utils.ProcStateDead,
		"app3:replica1":   process_utils.ProcStateStopped,
		"app3:stateboard": process_utils.ProcStateRunning,
	}

	getStatus = func(instCtx *InstanceCtx) process_utils.ProcessState {
		return statuses[GetAppInstanceName(*instCtx)]
	}

	cases := []struct {
		name     string
		tf       func([]InstanceCtx) []string
		expected []string
	}{
		{
			name: "active instances",
			tf:   ExtractActiveInstanceNames,
			expected: []string{
				"single_run",
				"app1:master", "app1:replica", "app3:stateboard",
			},
		},
		{
			name: "inactive instances",
			tf:   ExtractInactiveInstanceNames,
			expected: []string{
				"single_stopped",
				"app2:master", "app2:replica1", "app2:replica2",
				"app3:master", "app3:replica1",
			},
		},
		{
			name:     "active apps",
			tf:       ExtractActiveAppNames,
			expected: []string{"single_run", "app1", "app3"},
		},
		{
			name:     "inactive apps",
			tf:       ExtractInactiveAppNames,
			expected: []string{"single_stopped", "app2", "app3"},
		},
		{
			name:     "all apps",
			tf:       ExtractAppNames,
			expected: []string{"single_run", "app1", "single_stopped", "app2", "app3"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.tf(instances))
		})
	}
}
