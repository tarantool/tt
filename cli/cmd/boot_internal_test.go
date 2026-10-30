package cmd

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// restoreProcessState puts back the package state and the process logger a
// test of the boot phases replaces.
func restoreProcessState(t *testing.T) {
	t.Helper()

	previousRoot, previousCtx, previousInjected := rootCmd, cmdCtx, InjectedCmds
	previousFlavour := flavour
	previousProvider := integrityprovider.Get()
	previousLogger := slog.Default()

	t.Cleanup(func() {
		rootCmd, cmdCtx, InjectedCmds = previousRoot, previousCtx, previousInjected
		flavour = previousFlavour

		integrityprovider.Set(previousProvider)

		slog.SetDefault(previousLogger)
	})
}

// TestBootParsesGlobalFlags checks that Boot builds a root with the global
// flags and no subcommands, parses them and makes the root the process's.
//
//nolint:paralleltest // Replaces the package root and the process logger.
func TestBootParsesGlobalFlags(t *testing.T) {
	restoreProcessState(t)

	root, err := Boot(BootOptions{Args: []string{"-V", "--no-prompt", "version", "-I"}})
	require.NoError(t, err)

	assert.Same(t, rootCmd, root)
	assert.Empty(t, root.Commands())
	assert.True(t, cmdCtx.Cli.Verbose)
	assert.True(t, cmdCtx.Cli.NoPrompt)
	assert.False(t, cmdCtx.Cli.ForceInternal, "flags after the command are not the root's")
	assert.True(t, slog.Default().Enabled(t.Context(), slog.LevelDebug))
}

// TestBootSetsTheIntegrityProvider checks that Boot makes the provider it
// is given the provider of the process - the root and the commands built
// after it have its flags, and Configure checks with it - and that booting
// without one goes back to the disabled provider.
//
//nolint:paralleltest // Replaces the package root and the integrity provider.
func TestBootSetsTheIntegrityProvider(t *testing.T) {
	restoreProcessState(t)

	key := filepath.Join(t.TempDir(), "missing.pem")

	root, err := Boot(BootOptions{
		Args:      []string{"--integrity-check", key, "--integrity-check-period", "5"},
		Integrity: integrity.NewRSAProvider(),
	})
	require.NoError(t, err)

	assert.NotNil(t, root.Flags().Lookup("integrity-check"))
	assert.Equal(t, key, cmdCtx.Cli.IntegrityCheck)
	assert.Equal(t, 5, cmdCtx.Cli.IntegrityCheckPeriod)
	assert.True(t, instanceStartHasFlag(t, "integrity-check-period"),
		"the commands are built with the provider in effect")

	err = Configure()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integrity check failed")

	root, err = Boot(BootOptions{Args: nil})
	require.NoError(t, err)

	assert.Nil(t, root.Flags().Lookup("integrity-check"))
	assert.Nil(t, root.Flags().Lookup("integrity-check-period"))
	assert.False(t, instanceStartHasFlag(t, "integrity-check-period"))
}

// instanceStartHasFlag reports whether a new tt instance start has the flag
// name.
func instanceStartHasFlag(t *testing.T, name string) bool {
	t.Helper()

	start, _, err := NewInstanceCmd().Find([]string{"start"})
	require.NoError(t, err)
	require.Equal(t, "start", start.Name())

	return start.Flags().Lookup(name) != nil
}

// TestBuiltinCommandsAfterBoot checks that building the commands after the
// global flags are parsed leaves the parsed values alone: a command that
// binds a flag to the same variable must not reset it to its default.
//
//nolint:paralleltest // Replaces the package root and the process logger.
func TestBuiltinCommandsAfterBoot(t *testing.T) {
	restoreProcessState(t)

	root, err := Boot(BootOptions{Args: []string{
		"-V", "--no-prompt", "-I", "start",
	}})
	require.NoError(t, err)

	root.AddCommand(BuiltinCommands()...)

	assert.True(t, cmdCtx.Cli.Verbose)
	assert.True(t, cmdCtx.Cli.NoPrompt)
	assert.True(t, cmdCtx.Cli.ForceInternal)
}

// TestBootRefusesRemovedFlags checks that the flags that chose a tt.yaml are
// gone, and so is -s, which kept tt from running the tt of its bin_dir: -S,
// -L, -c and -s, long and short, are unknown flags of the root.
//
//nolint:paralleltest // Replaces the package root and the process logger.
func TestBootRefusesRemovedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-S"}, {"--system"}, {"-L", "dir"}, {"--local", "dir"}, {"-c", "tt.yaml"},
		{"--cfg", "tt.yaml"}, {"-s"}, {"--self"},
	} {
		restoreProcessState(t)

		root, err := Boot(BootOptions{Args: append(args, "version")})
		require.NoError(t, err)

		err = root.ParseFlags(append(args, "version"))
		require.ErrorContains(t, err, "unknown", args)
	}
}

// TestConfigureFindsTheProject checks that Configure finds the project from a
// directory inside it and ignores a tt.yaml there, broken as it is.
//
//nolint:paralleltest // Replaces the package context and the working directory.
func TestConfigureFindsTheProject(t *testing.T) {
	restoreProcessState(t)
	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	project := t.TempDir()
	nested := filepath.Join(project, "nested")
	require.NoError(t, os.Mkdir(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "app.manifest.toml"),
		[]byte("manifest_version = '0.1'\n"), 0o644))

	for _, dir := range []string{project, nested} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tt.yaml"), []byte("[broken"),
			0o644))
	}

	t.Chdir(nested)

	cmdCtx = cmdcontext.CmdCtx{}

	require.NoError(t, Configure())
	assert.Equal(t, project, cmdCtx.Cli.ProjectDir)
	assert.NotNil(t, cmdCtx.Integrity.Repository)
}

// TestBuiltinCommandsAreLegacy checks that every builtin is marked legacy.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestBuiltinCommandsAreLegacy(t *testing.T) {
	commands := BuiltinCommands()
	require.Len(t, commands, 24)

	for _, command := range commands {
		assert.True(t, isLegacy(command), command.Name())
	}
}

// TestInjectCommandsReplaces checks that an injected command replaces the
// top-level command of its name and is marked legacy.
//
//nolint:paralleltest // Replaces InjectedCmds.
func TestInjectCommandsReplaces(t *testing.T) {
	restoreProcessState(t)

	injected := &cobra.Command{Use: "version", Short: "injected"}
	extra := &cobra.Command{Use: "extra"}

	InjectedCmds = []*cobra.Command{injected, extra}

	root := &cobra.Command{Use: "tt"}
	root.AddCommand(BuiltinCommands()...)

	require.NoError(t, InjectCommands(root))

	found, _, err := root.Find([]string{"version"})
	require.NoError(t, err)
	assert.Same(t, injected, found)
	assert.True(t, isLegacy(injected))
	assert.True(t, isLegacy(extra))
	assert.Len(t, root.Commands(), 25)

	require.ErrorIs(t, InjectCommands(nil), errNilCommandRoot)
}
