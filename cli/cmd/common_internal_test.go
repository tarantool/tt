package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/configure"
)

// TestInternalRunE checks that the adapter records the command's name in
// the CmdCtx - tt binary install, tt binary uninstall and tt binary search take the program from
// it - and gives the internal function the process's CmdCtx and the
// arguments.
func TestInternalRunE(t *testing.T) {
	previous := cmdCtx

	t.Cleanup(func() { cmdCtx = previous })

	var (
		gotCtx  *cmdcontext.CmdCtx
		gotArgs []string
	)

	runE := internalRunE(func(ctx *cmdcontext.CmdCtx, args []string) error {
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
// environment at all; neither are the instance commands, which find the
// application from the project (TestInstanceCommandsOutsideProject).
func TestCheckConfig(t *testing.T) {
	const expected = configure.ConfigName +
		" not found, you need to create a tt environment config" +
		" or provide exact config location with --cfg option"

	assert.ErrorContains(t, internalCreateModule(&cmdcontext.CmdCtx{}, nil), expected)
}

// TestInstanceCommandsOutsideProject checks that the instance commands do
// not look for tt.yaml: run without a target outside of a project, they
// report that there is no project, and point out a tt2 environment, whose
// tt.yaml they do not read.
func TestInstanceCommandsOutsideProject(t *testing.T) {
	modules := []struct {
		name   string
		module internalFunc
	}{
		{"check", internalCheckModule},
		{"clean", internalCleanModule},
		{"kill", internalKillModule},
		{"log", internalLogModule},
		{"logrotate", internalLogrotateModule},
		{"restart", internalRestartModule},
		{"start", internalStartModule},
		{"status", internalStatusModule},
		{"stop", internalStopWithConfirmationModule},
	}

	dir := t.TempDir()
	t.Chdir(dir)

	for _, tc := range modules {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.module(&cmdcontext.CmdCtx{}, nil)
			require.ErrorIs(t, err, app.ErrNoProject)
			assert.NotErrorIs(t, err, app.ErrTT2Environment)
		})
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, configure.ConfigName), nil, 0o600))

	for _, tc := range modules {
		t.Run(tc.name+" in a tt2 environment", func(t *testing.T) {
			err := tc.module(&cmdcontext.CmdCtx{}, nil)
			require.ErrorIs(t, err, app.ErrNoProject)
			require.ErrorIs(t, err, app.ErrTT2Environment)
		})
	}
}

// TestInstanceCommandsRefuseBareApp checks that an instance command refuses
// a bare application name with a hint, rather than acting on every instance
// of the application.
func TestInstanceCommandsRefuseBareApp(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := resolveInstances(&cmdcontext.CmdCtx{}, []string{"myapp"})
	require.ErrorIs(t, err, app.ErrInvalidTarget)
	assert.ErrorContains(t, err, "use myapp:<instance> or myapp:*")
}
