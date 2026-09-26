package cmd

import (
	"log/slog"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restoreProcessState puts back the package state and the process logger a
// test of the boot phases replaces.
func restoreProcessState(t *testing.T) {
	t.Helper()

	previousRoot, previousCtx, previousInjected := rootCmd, cmdCtx, InjectedCmds
	previousFlavour := flavour
	previousLogger := slog.Default()

	t.Cleanup(func() {
		rootCmd, cmdCtx, InjectedCmds = previousRoot, previousCtx, previousInjected
		flavour = previousFlavour

		slog.SetDefault(previousLogger)
	})
}

// TestBootParsesGlobalFlags checks that Boot builds a root with the global
// flags and no subcommands, parses them and makes the root the process's.
//
//nolint:paralleltest // Replaces the package root and the process logger.
func TestBootParsesGlobalFlags(t *testing.T) {
	restoreProcessState(t)

	root, err := Boot(BootOptions{Args: []string{"-V", "--no-prompt", "version", "-s"}})
	require.NoError(t, err)

	assert.Same(t, rootCmd, root)
	assert.Empty(t, root.Commands())
	assert.True(t, cmdCtx.Cli.Verbose)
	assert.True(t, cmdCtx.Cli.NoPrompt)
	assert.False(t, cmdCtx.Cli.IsSelfExec, "flags after the command are not the root's")
	assert.True(t, slog.Default().Enabled(t.Context(), slog.LevelDebug))
}

// TestBuiltinCommandsAfterBoot checks that building the commands after the
// global flags are parsed leaves the parsed values alone: a command that
// binds a flag to the same variable must not reset it to its default.
//
//nolint:paralleltest // Replaces the package root and the process logger.
func TestBuiltinCommandsAfterBoot(t *testing.T) {
	restoreProcessState(t)

	root, err := Boot(BootOptions{Args: []string{
		"-V", "--no-prompt", "-I", "-s", "--cfg", "x.yaml", "start",
	}})
	require.NoError(t, err)

	root.AddCommand(BuiltinCommands()...)

	assert.True(t, cmdCtx.Cli.Verbose)
	assert.True(t, cmdCtx.Cli.NoPrompt)
	assert.True(t, cmdCtx.Cli.ForceInternal)
	assert.True(t, cmdCtx.Cli.IsSelfExec)
	assert.Equal(t, "x.yaml", cmdCtx.Cli.ConfigPath)
}

// TestBuiltinCommandsAreLegacy checks that every builtin is marked legacy.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestBuiltinCommandsAreLegacy(t *testing.T) {
	commands := BuiltinCommands()
	require.Len(t, commands, 34)

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
	assert.Len(t, root.Commands(), 35)

	require.ErrorIs(t, InjectCommands(nil), errNilCommandRoot)
}
