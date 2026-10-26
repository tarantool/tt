package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
)

// withConfirmation makes the standard input a pipe holding input, a
// terminal or not as terminal says, and --yes as yes says, for the test.
// It returns the pipe the standard input reads, to see what is left of it.
func withConfirmation(t *testing.T, input string, terminal, yes bool) *os.File {
	t.Helper()

	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	_, err = writer.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	previousStdin, previousTerminal, previousYes := os.Stdin, stdinIsTerminal, instanceYes

	t.Cleanup(func() {
		os.Stdin, stdinIsTerminal, instanceYes = previousStdin, previousTerminal, previousYes
		_ = reader.Close()
	})

	os.Stdin = reader
	stdinIsTerminal = func() bool { return terminal }
	instanceYes = yes

	return reader
}

// TestConfirm pins how the commands of tt instance confirm (RFC 4.3): --yes
// confirms without a prompt; a terminal is asked, and its no is no failure;
// without a terminal the command fails with code 1, pointing at --yes, and
// leaves the standard input unread, so an answer piped in confirms nothing.
//
//nolint:paralleltest // Replaces the standard input and --yes.
func TestConfirm(t *testing.T) {
	t.Run("--yes", func(t *testing.T) {
		withConfirmation(t, "n\n", false, true)

		confirmed, err := confirm("Confirm stop of all instances")
		require.NoError(t, err)
		assert.True(t, confirmed)
	})

	for answer, want := range map[string]bool{"y\n": true, "maybe\nyes\n": true, "n\n": false} {
		t.Run("terminal answers "+strconv.Quote(answer), func(t *testing.T) {
			withConfirmation(t, answer, true, false)

			confirmed, err := confirm("Confirm stop of all instances")
			require.NoError(t, err)
			assert.Equal(t, want, confirmed)
		})
	}

	t.Run("no terminal", func(t *testing.T) {
		stdin := withConfirmation(t, "y\n", false, false)

		confirmed, err := confirm("Confirm stop of all instances")
		require.ErrorIs(t, err, errNotTerminal)
		require.ErrorContains(t, err, "pass --yes")
		assert.False(t, confirmed)
		assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))

		left, err := io.ReadAll(stdin)
		require.NoError(t, err)
		assert.Equal(t, "y\n", string(left), "the standard input was read")
	})

	t.Run("the terminal closes", func(t *testing.T) {
		withConfirmation(t, "", true, false)

		_, err := confirm("Confirm stop of all instances")
		require.ErrorIs(t, err, errNoAnswer)
		require.ErrorContains(t, err, "pass --yes")
	})
}

// TestConfirmingCommands pins that every command of tt instance that asks
// for a confirmation fails without a terminal and --yes, before it acts on
// anything; and that --no-prompt confirms nothing, not even for stop.
//
//nolint:paralleltest // Replaces the standard input and --yes.
func TestConfirmingCommands(t *testing.T) {
	externalProject(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	cmdCtx.Cli.NoPrompt = true

	for name, module := range map[string]internalFunc{
		"stop":    internalStopWithConfirmationModule,
		"restart": internalRestartModule,
		"kill":    internalKillModule,
		"clean":   internalCleanModule,
	} {
		t.Run(name, func(t *testing.T) {
			withConfirmation(t, "y\n", false, false)

			if name == "clean" {
				// clean asks only when there is something to remove.
				fillLogDir(t, cmdCtx, "myapp:storage-1")
			}

			err := module(cmdCtx, []string{"myapp:*"})
			require.ErrorIs(t, err, errNotTerminal)
			assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
		})
	}
}

// TestCleanWithNothingToRemoveAsksNothing pins that tt instance clean asks
// no question when it has nothing to remove: it needs neither a terminal nor
// --yes then.
//
//nolint:paralleltest // Replaces the standard input and --yes.
func TestCleanWithNothingToRemoveAsksNothing(t *testing.T) {
	externalProject(t)

	stdin := withConfirmation(t, "y\n", false, false)

	require.NoError(t, internalCleanModule(&cmdcontext.CmdCtx{}, []string{"myapp:*"}))

	left, err := io.ReadAll(stdin)
	require.NoError(t, err)
	assert.Equal(t, "y\n", string(left), "the standard input is not read")
}

// fillLogDir puts a log file into the log directory of the instance target
// names, for tt instance clean to have something to remove.
func fillLogDir(t *testing.T, cmdCtx *cmdcontext.CmdCtx, target string) {
	t.Helper()

	instances, err := resolveInstances(cmdCtx, []string{target})
	require.NoError(t, err)
	require.Len(t, instances, 1)

	logDir := instances[0].LogDir
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "tt.log"), []byte("log\n"), 0o644))
}
