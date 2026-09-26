package extmod_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
)

// TestModuleExitStatus pins that an external module's exit status becomes
// tt's exit code, and that tt does not print anything of its own for it.
func TestModuleExitStatus(t *testing.T) {
	t.Parallel()

	// run mounts a module whose main is script and runs it.
	run := func(t *testing.T, script string) error {
		t.Helper()

		dir := t.TempDir()
		writeModule(t, dir, "status", script, "version: 1.0.0\nhelp: x\nmain: main\n")

		tree := newTree(t)

		_, err := tree.mount(dir)
		require.NoError(t, err)

		cmd, err := tree.run("status")
		if err != nil {
			assert.True(t, cmd.SilenceErrors, "the core reports the error")
			assert.Empty(t, tree.errOut.String())
		}

		return err
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, run(t, "exit 0"))
	})

	t.Run("non-zero status", func(t *testing.T) {
		t.Parallel()

		err := run(t, "exit 7")
		require.Error(t, err)
		assert.Equal(t, 7, sdk.ExitCode(err))
		assert.True(t, exitcode.IsSilent(err))
	})

	t.Run("status 1", func(t *testing.T) {
		t.Parallel()

		err := run(t, "exit 1")
		require.Error(t, err)
		assert.Equal(t, 1, sdk.ExitCode(err))
		assert.True(t, exitcode.IsSilent(err))
	})

	t.Run("killed by a signal", func(t *testing.T) {
		t.Parallel()

		err := run(t, "kill -9 $$")
		require.Error(t, err)
		assert.Equal(t, 255, sdk.ExitCode(err))
		assert.True(t, exitcode.IsSilent(err))
	})

	t.Run("cannot be started", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		main := writeModule(t, dir, "status", "", "version: 1.0.0\nhelp: x\nmain: main\n")

		// The interpreter does not exist.
		require.NoError(t, os.WriteFile(main, []byte("#!/nonexistent/sh\n"), 0o755))

		tree := newTree(t)

		_, err := tree.mount(dir)
		require.NoError(t, err)

		_, err = tree.run("status")
		require.Error(t, err)
		assert.Equal(t, 1, sdk.ExitCode(err))
		assert.False(t, exitcode.IsSilent(err), "tt must say why the module did not run")
	})
}
