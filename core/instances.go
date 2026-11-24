package core

import (
	"context"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/running"
)

// LocalInstances returns the instances targets select as tt instance status
// selects them (cmd.StatusInstances), each with where its process runs, its
// run directory, its logs and its crashes. An application or an instance
// the lookup does not find is sdk.ErrNotFound as well.
func (s *services) LocalInstances(ctx context.Context, targets []string,
	lookup sdk.AppLookup,
) ([]sdk.LocalInstance, error) {
	s.mustBeReady("LocalInstances")

	insts, err := cmd.StatusInstances(ctx, lookup, targets)
	if err != nil {
		return nil, connectError(err)
	}

	found := make([]sdk.LocalInstance, 0, len(insts))

	for i := range insts {
		found = append(found, localInstance(&insts[i]))
	}

	return found, nil
}

// localInstance describes inst as the SDK has it, with why its crashes
// cannot be read, if they cannot.
func localInstance(inst *running.InstanceCtx) sdk.LocalInstance {
	recorded, err := running.ReadCrashes(inst)
	crashes := make([]sdk.Crash, 0, len(recorded))

	for _, crash := range recorded {
		crashes = append(crashes, sdk.Crash{
			At: crash.At, PID: crash.PID, Signal: crash.Signal, CoreDumped: crash.CoreDumped,
		})
	}

	return sdk.LocalInstance{
		Name:         running.GetAppInstanceName(*inst),
		WorkDir:      running.WorkDir(inst),
		RunDir:       inst.RunDir,
		Log:          inst.Log,
		TarantoolLog: running.TarantoolLog(inst),
		Crashes:      crashes,
		CrashesErr:   err,
	}
}
