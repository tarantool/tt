package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/configure"
)

// TestRunModuleFuncE checks that the adapter records the command's name in
// the CmdCtx - tt install, tt uninstall and tt search take the program from
// it - and gives the internal function the process's CmdCtx and the
// arguments.
func TestRunModuleFuncE(t *testing.T) {
	previous := cmdCtx

	t.Cleanup(func() { cmdCtx = previous })

	var (
		gotCtx  *cmdcontext.CmdCtx
		gotArgs []string
	)

	runE := RunModuleFuncE(func(ctx *cmdcontext.CmdCtx, args []string) error {
		gotCtx, gotArgs = ctx, args

		return nil
	})

	require.NoError(t, runE(&cobra.Command{Use: "tarantool"}, []string{"3.2.0"}))
	assert.Equal(t, "tarantool", cmdCtx.CommandName)
	assert.Same(t, &cmdCtx, gotCtx)
	assert.Equal(t, []string{"3.2.0"}, gotArgs)
}

// TestCheckConfig covers the commands that act on a tt environment and so
// cannot proceed without its config. tt run and tt test are deliberately not
// here: they act on the package in the working directory and need no
// environment at all.
func TestCheckConfig(t *testing.T) {
	const expected = configure.ConfigName +
		" not found, you need to create a tt environment config" +
		" or provide exact config location with --cfg option"

	cases := []struct {
		name string
		err  error
	}{
		{"binaries list", internalListModule(&cmdcontext.CmdCtx{}, nil)},
		{"binaries switch", internalSwitchModule(&cmdcontext.CmdCtx{}, nil)},
		{"check", internalCheckModule(&cmdcontext.CmdCtx{}, nil)},
		{"clean", internalCleanModule(&cmdcontext.CmdCtx{}, nil)},
		{"create", internalCreateModule(&cmdcontext.CmdCtx{}, nil)},
		{"install", internalInstallModule(&cmdcontext.CmdCtx{}, nil)},
		{"logrotate", internalLogrotateModule(&cmdcontext.CmdCtx{}, nil)},
		{"restart", internalRestartModule(&cmdcontext.CmdCtx{}, nil)},
		{"start", internalStartModule(&cmdcontext.CmdCtx{}, nil)},
		{"status", internalStatusModule(&cmdcontext.CmdCtx{}, nil)},
		{"stop", internalStopModule(&cmdcontext.CmdCtx{}, nil)},
		{"uninstall", InternalUninstallModule(&cmdcontext.CmdCtx{}, nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorContains(t, tc.err, expected)
		})
	}
}
