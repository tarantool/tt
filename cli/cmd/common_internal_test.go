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

// TestCreateWithoutConfig checks that tt create needs no tt environment: it
// creates the application in the working directory, from a template given
// by its path.
func TestCreateWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	template := filepath.Join(dir, "templates", "basic")
	require.NoError(t, os.MkdirAll(template, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(template, "init.lua"), []byte("return\n"),
		0o644))

	previousName, previousSilent, previousVars := appName, nonInteractiveMode, varsFromCli

	t.Cleanup(func() {
		appName, nonInteractiveMode, varsFromCli = previousName, previousSilent, previousVars
	})

	appName, nonInteractiveMode, varsFromCli = "app1", true, &[]string{}

	require.NoError(t, internalCreateModule(&cmdcontext.CmdCtx{}, []string{"./templates/basic"}))
	assert.FileExists(t, filepath.Join(dir, "app1", "init.lua"))
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
		{"logs", internalLogsModule},
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

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tt.yaml"), nil, 0o600))

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
	assert.ErrorContains(t, err, "use myapp:<INSTANCE> or myapp:*")
}
