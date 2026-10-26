package cmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// TestRestartChecksBeforeStop pins that tt instance restart checks that
// every target can start before it stops any: a target that cannot start
// fails the command with code 1, naming it, and every instance runs on.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestRestartChecksBeforeStop(t *testing.T) {
	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	dir := filepath.Join(t.TempDir(), "myapp")
	writeFiles(t, dir, map[string]string{
		".templates/default/instances.yaml": "worker-1:\n  binary: sh\n" +
			"worker-2:\n  binary: sh\n",
		"bin/.keep": "",
	})
	withProjectDir(t, dir)
	t.Chdir(dir)

	cmdCtx := &cmdcontext.CmdCtx{}

	insts, err := resolveProcesses(cmdCtx, nil)
	require.NoError(t, err)
	require.Len(t, insts, 2)

	pids := []int{fakeWatchdog(t, &insts[0]), fakeWatchdog(t, &insts[1])}

	// worker-2 cannot start any more: its binary is gone.
	writeFiles(t, dir, map[string]string{
		".templates/default/instances.yaml": "worker-1:\n  binary: sh\n" +
			"worker-2:\n  binary: ./bin/gone\n",
	})

	NewRestartCmd()

	instanceYes = true

	t.Cleanup(func() { instanceYes = false })

	for _, args := range [][]string{nil, {"myapp:*"}, {"myapp:worker-1,myapp:worker-2"}} {
		err = internalRestartModule(cmdCtx, args)
		require.Error(t, err, args)
		assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err), args)
		require.ErrorContains(t, err, `instance "worker-2" of "myapp"`, args)

		for i, pid := range pids {
			require.NoError(t, syscall.Kill(pid, 0), "%v: %s is not stopped", args,
				insts[i].InstName)
		}

		_, err = os.Stat(insts[0].PIDFile)
		require.NoError(t, err)
	}
}
