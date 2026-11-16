package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/status"
)

// TestStatusTargetsOfAScope pins that tt instance status --scope user with
// no target takes every application of the scope, and the project the
// command runs in without --scope.
//
//nolint:paralleltest // The instance flags are package state.
func TestStatusTargetsOfAScope(t *testing.T) {
	version, project := lookupFixture(t)

	names := func(insts []running.InstanceCtx) []string {
		got := make([]string, 0, len(insts))
		for _, inst := range insts {
			got = append(got, running.GetAppInstanceName(inst)+" "+inst.AppDir)
		}

		return got
	}

	cmdCtx := &cmdcontext.CmdCtx{}

	insts, err := resolveStatusTargets(cmdCtx, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"myapp:storage-1 " + project}, names(insts))

	instanceFlags.Scope = "user"

	insts, err = resolveStatusTargets(cmdCtx, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"myapp:storage-1 " + version, "myapp:storage-2 " + version},
		names(insts))

	// The system scope has nothing installed: no instances, and no error.
	instanceFlags.Scope = "system"

	insts, err = resolveStatusTargets(cmdCtx, nil)
	require.NoError(t, err)
	require.Nil(t, insts)
}

func TestStatusInstanceTimeoutFlag(t *testing.T) {
	previous := opts

	t.Cleanup(func() { opts = previous })

	cmd := NewStatusCmd()

	require.Equal(t, status.DefaultInstanceTimeout, opts.instanceTimeout)
	require.NoError(t, cmd.Flags().Parse([]string{"--instance-timeout", "0"}))
	require.Zero(t, opts.instanceTimeout)
	require.NoError(t, cmd.Flags().Parse([]string{"--instance-timeout", "250ms"}))
	require.Equal(t, 250*time.Millisecond, opts.instanceTimeout)
	require.NoError(t, cmd.Flags().Parse([]string{"--instance-timeout", "-1s"}))
	require.EqualError(t, internalStatusModule(&cmdcontext.CmdCtx{}, nil),
		"instance timeout must not be negative")
}
