package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/install"
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

	// --no-prompt asks nothing, on a terminal either: the confirmation is not
	// given, and the command fails as it does off a terminal; --yes gives it.
	for _, yes := range []bool{false, true} {
		t.Run("--no-prompt, --yes "+strconv.FormatBool(yes), func(t *testing.T) {
			stdin := withConfirmation(t, "y\n", true, yes)
			withNoPrompt(t)

			confirmed, err := confirm("Confirm stop of all instances")
			assert.Equal(t, yes, confirmed)

			if yes {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, errNoPrompt)
			require.ErrorIs(t, err, errNoAnswer)
			require.ErrorContains(t, err, "pass --yes")
			assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))

			left, err := io.ReadAll(stdin)
			require.NoError(t, err)
			assert.Equal(t, "y\n", string(left), "the standard input was read")
		})
	}
}

// withNoPrompt sets --no-prompt, the global flag, for the test.
func withNoPrompt(t *testing.T) {
	t.Helper()

	previous := cmdCtx.Cli.NoPrompt

	t.Cleanup(func() { cmdCtx.Cli.NoPrompt = previous })

	cmdCtx.Cli.NoPrompt = true
}

// TestPackageConfirmNoPrompt pins that the confirmations of tt package
// install and uninstall are not asked under --no-prompt, and are not given.
//
//nolint:paralleltest // Replaces the standard input and --no-prompt.
func TestPackageConfirmNoPrompt(t *testing.T) {
	stdin := withConfirmation(t, "y\n", true, false)
	withNoPrompt(t)

	assert.False(t, packageConfirm("remove monitoring?"))

	left, err := io.ReadAll(stdin)
	require.NoError(t, err)
	assert.Equal(t, "y\n", string(left), "the standard input was read")
}

// TestBinaryInstallConfirms pins that tt binary install confirms its
// questions as tt instance does: --yes of install and of its commands,
// before the command or after it, answers yes; a terminal is asked; and
// without a terminal, or under --no-prompt, nothing is asked and the
// standard input is left unread.
//
//nolint:paralleltest // Replaces the standard input and --yes.
func TestBinaryInstallConfirms(t *testing.T) {
	newInstall := func(t *testing.T) (*cobra.Command, *install.InstallCtx) {
		t.Helper()

		restoreProcessState(t)

		var installCtx install.InstallCtx

		command := newBinaryInstallCmd(&binary.BinaryContext{}, &installCtx)
		require.NotNil(t, installCtx.Confirm)

		return command, &installCtx
	}

	for _, args := range [][]string{
		{"tarantool", "-y"}, {"tarantool", "--yes"}, {"-y", "tarantool"}, {"tt", "-y"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command, installCtx := newInstall(t)
			// The flags of the command set --yes to false as they are made.
			withConfirmation(t, "n\n", false, false)

			found, rest, err := command.Find(args)
			require.NoError(t, err)
			require.NoError(t, found.ParseFlags(rest))

			confirmed, err := installCtx.Confirm("Proceed without checking?")
			require.NoError(t, err)
			assert.True(t, confirmed)
		})
	}

	t.Run("terminal", func(t *testing.T) {
		_, installCtx := newInstall(t)
		withConfirmation(t, "n\n", true, false)

		confirmed, err := installCtx.Confirm("Proceed without checking?")
		require.NoError(t, err)
		assert.False(t, confirmed)
	})

	for name, noPrompt := range map[string]bool{
		"off a terminal": false, "--no-prompt on a terminal": true,
	} {
		t.Run(name, func(t *testing.T) {
			_, installCtx := newInstall(t)
			stdin := withConfirmation(t, "y\n", noPrompt, false)

			want := errNotTerminal

			if noPrompt {
				withNoPrompt(t)

				want = errNoPrompt
			}

			confirmed, err := installCtx.Confirm("Proceed without checking?")
			require.ErrorIs(t, err, want)
			require.ErrorContains(t, err, "pass --yes")
			assert.False(t, confirmed)

			left, err := io.ReadAll(stdin)
			require.NoError(t, err)
			assert.Equal(t, "y\n", string(left), "the standard input was read")
		})
	}
}

// TestConfirmingCommands pins that every command of tt instance that asks
// for a confirmation fails without a terminal and --yes, before it acts on
// anything; and that --no-prompt confirms nothing, not even on a terminal.
//
//nolint:paralleltest // Replaces the standard input and --yes.
func TestConfirmingCommands(t *testing.T) {
	externalProject(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	// The wait restart checks first, as its flags set it.
	previousWait := startWait

	t.Cleanup(func() { startWait = previousWait })

	startWait = waitOptions{timeout: running.DefaultStartTimeout, noWait: false}

	for _, noPrompt := range []bool{false, true} {
		for name, module := range map[string]internalFunc{
			"stop":    internalStopWithConfirmationModule,
			"restart": internalRestartModule,
			"kill":    internalKillModule,
			"clean":   internalCleanModule,
		} {
			t.Run(name+" --no-prompt "+strconv.FormatBool(noPrompt), func(t *testing.T) {
				// On a terminal under --no-prompt, off one without it.
				withConfirmation(t, "y\n", noPrompt, false)

				want := errNotTerminal

				if noPrompt {
					withNoPrompt(t)

					want = errNoPrompt
				}

				if name == "clean" {
					// clean asks only when there is something to remove.
					fillLogDir(t, cmdCtx, "myapp:storage-1")
				}

				err := module(cmdCtx, []string{"myapp:*"})
				require.ErrorIs(t, err, want)
				assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
			})
		}
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
