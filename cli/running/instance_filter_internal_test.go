package running

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/v3/cli/process_utils"
)

// TestInstanceActivity pins what completion counts as a running instance:
// one whose process runs, not one whose process died or never ran.
func TestInstanceActivity(t *testing.T) {
	savedGetStatus := getStatus

	t.Cleanup(func() { getStatus = savedGetStatus })

	statuses := map[string]process_utils.ProcessState{
		"app:running": process_utils.ProcStateRunning,
		"app:stopped": process_utils.ProcStateStopped,
		"app:dead":    process_utils.ProcStateDead,
	}

	getStatus = func(instCtx *InstanceCtx) process_utils.ProcessState {
		return statuses[GetAppInstanceName(*instCtx)]
	}

	for name, active := range map[string]bool{
		"running": true,
		"stopped": false,
		"dead":    false,
	} {
		t.Run(name, func(t *testing.T) {
			inst := InstanceCtx{AppName: "app", InstName: name}

			assert.Equal(t, active, IsInstanceActive(&inst))
			assert.Equal(t, !active, IsInstanceInactive(&inst))
		})
	}
}
