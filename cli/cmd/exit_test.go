package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/util"
)

// exitRun is what one run of a command tree left behind.
type exitRun struct {
	code   int    // The process exit code the root decided on.
	logged string // What was logged, one line per record.
	cobra  string // What cobra printed to its error stream.
	out    string // What was printed to the output stream (usage).
}

// runExit executes a root with one subcommand whose RunE is run, as the tt
// root does, and reports what came out of it.
func runExit(t *testing.T, run func(*cobra.Command, []string) error, args ...string) exitRun {
	t.Helper()

	var logged, cobraErr, out bytes.Buffer

	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	root := &cobra.Command{Use: "tt"}
	root.AddCommand(&cobra.Command{Use: "sub", Args: cobra.NoArgs, RunE: run})
	root.SetErr(&cobraErr)
	root.SetOut(&out)
	root.SetArgs(args)

	code := sdk.ExitOK

	cmd, err := root.ExecuteC()
	if err != nil {
		code = reportError(cmd, err)
	}

	return exitRun{
		code:   code,
		logged: logged.String(),
		cobra:  cobraErr.String(),
		out:    out.String(),
	}
}

// refused is a dial failure: a failure of the system, not of the request.
var refused = &net.OpError{
	Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
}

// TestReportError pins what the root does with the error a command ends
// with: print it once, through the logger, and exit with its code.
//
//nolint:paralleltest // Replaces slog.Default for the duration of each case.
func TestReportError(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, nil)
		}, "sub")

		assert.Equal(t, 0, got.code)
		assert.Empty(t, got.logged)
		assert.Empty(t, got.cobra)
	})

	t.Run("command error is logged once, without usage", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, errors.New("instance is not running"))
		}, "sub")

		assert.Equal(t, 1, got.code)
		assert.Equal(t, 1, strings.Count(got.logged, "\n"), got.logged)
		assert.Contains(t, got.logged, `level=ERROR msg="instance is not running"`)
		assert.Empty(t, got.cobra, "cobra must not print it a second time")
		assert.Empty(t, got.out, "no usage for a failure of the command itself")
	})

	t.Run("an uncoded system failure exits 2", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, fmt.Errorf("connecting: %w", refused))
		}, "sub")

		assert.Equal(t, 2, got.code)
		assert.Equal(t, 1, strings.Count(got.logged, "\n"), got.logged)
		assert.Contains(t, got.logged, "connecting: dial tcp: connect: connection refused")
	})

	t.Run("a system failure beneath code 1 exits 2", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, sdk.WithCode(sdk.ExitFailure,
				fmt.Errorf("resolving: %w", refused)))
		}, "sub")

		assert.Equal(t, 2, got.code)
		assert.Equal(t, 1, strings.Count(got.logged, "\n"), got.logged)
		assert.Contains(t, got.logged, "resolving: dial tcp: connect: connection refused")
	})

	t.Run("a permission refusal exits 2", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, fmt.Errorf("writing pid file: %w",
				&os.PathError{Op: "open", Path: "/run/tt.pid", Err: syscall.EACCES}))
		}, "sub")

		assert.Equal(t, 2, got.code)
	})

	t.Run("a missing file stays 1", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, fmt.Errorf("reading: %w",
				&os.PathError{Op: "open", Path: "tt.yaml", Err: syscall.ENOENT}))
		}, "sub")

		assert.Equal(t, 1, got.code)
	})

	t.Run("explicit code stands", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, sdk.WithCode(sdk.ExitPartial, refused))
		}, "sub")

		assert.Equal(t, 3, got.code)
	})

	t.Run("arg error prints the usage of the command", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, fmt.Errorf("parsing: %w", util.NewArgError("bad value")))
		}, "sub")

		assert.Equal(t, 1, got.code)
		assert.Contains(t, got.logged, `level=ERROR msg="bad value"`)
		assert.Contains(t, got.out, "tt sub [flags]")
		assert.Empty(t, got.cobra)
	})

	t.Run("sdk usage error prints the usage of the command", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, fmt.Errorf("parsing: %w", sdk.Usagef("bad value %d", 7)))
		}, "sub")

		assert.Equal(t, 1, got.code)
		assert.Equal(t, 1, strings.Count(got.logged, "\n"), got.logged)
		assert.Contains(t, got.logged, `level=ERROR msg="bad value 7"`)
		assert.Contains(t, got.out, "tt sub [flags]")
		assert.Empty(t, got.cobra)
	})

	t.Run("sdk usage error keeps a deeper code", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, sdk.WithUsage(sdk.WithCode(sdk.ExitPartial,
				errors.New("some targets"))))
		}, "sub")

		assert.Equal(t, 3, got.code)
		assert.Contains(t, got.out, "tt sub [flags]")
	})

	t.Run("silent error keeps its code and prints nothing", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, sdk.WithCode(7, exitcode.Silent(errors.New("module"))))
		}, "sub")

		assert.Equal(t, 7, got.code)
		assert.Empty(t, got.logged)
		assert.Empty(t, got.cobra)
	})

	t.Run("aborted by the user exits 1 silently", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			return commandError(cmd, fmt.Errorf("installing: %w", util.ErrCmdAbort))
		}, "sub")

		assert.Equal(t, 1, got.code)
		assert.Empty(t, got.logged)
	})

	t.Run("an error cobra found is printed by cobra alone", func(t *testing.T) {
		got := runExit(t, func(cmd *cobra.Command, _ []string) error {
			require.Fail(t, "the command must not run")

			return nil
		}, "sub", "--no-such-flag")

		assert.Equal(t, 1, got.code)
		assert.Empty(t, got.logged, "the root must not repeat what cobra printed")
		assert.Equal(t, 1, strings.Count(got.cobra, "unknown flag: --no-such-flag"), got.cobra)
		assert.Contains(t, got.out, "tt sub [flags]", "cobra prints the usage after its error")
	})
}
