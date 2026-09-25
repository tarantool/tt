package modules_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/modules"
)

// writeModule writes an executable shell script with body into a temporary
// directory and returns its path.
func writeModule(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "main")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755))

	return path
}

// TestRunExecExitStatus pins that an external module's exit status becomes
// tt's exit code, and that tt does not print anything of its own for it.
func TestRunExecExitStatus(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, modules.RunExec(writeModule(t, "exit 0"), nil))
	})

	t.Run("non-zero status", func(t *testing.T) {
		t.Parallel()

		err := modules.RunExec(writeModule(t, "exit 7"), nil)
		require.Error(t, err)
		assert.Equal(t, 7, sdk.ExitCode(err))
		assert.True(t, exitcode.IsSilent(err))
	})

	t.Run("status 1", func(t *testing.T) {
		t.Parallel()

		err := modules.RunExec(writeModule(t, "exit 1"), nil)
		require.Error(t, err)
		assert.Equal(t, 1, sdk.ExitCode(err))
		assert.True(t, exitcode.IsSilent(err))
	})

	t.Run("killed by a signal", func(t *testing.T) {
		t.Parallel()

		err := modules.RunExec(writeModule(t, "kill -9 $$"), nil)
		require.Error(t, err)
		assert.Equal(t, 255, sdk.ExitCode(err))
		assert.True(t, exitcode.IsSilent(err))
	})

	t.Run("cannot be started", func(t *testing.T) {
		t.Parallel()

		err := modules.RunExec(filepath.Join(t.TempDir(), "missing"), nil)
		require.Error(t, err)
		assert.Equal(t, 1, sdk.ExitCode(err))
		assert.False(t, exitcode.IsSilent(err), "tt must say why the module did not run")
	})
}
