package cmd

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/v3/cli/logging"
)

// unknownRun is what one run of a command tree left behind.
type unknownRun struct {
	code   int    // The process exit code the root decided on.
	logged string // What was logged, as tt renders it.
	out    string // What was printed to the output stream (help).
}

// runUnknown executes args on root, as Run does, logging as tt does.
func runUnknown(t *testing.T, root *cobra.Command, args ...string) unknownRun {
	t.Helper()

	var logged, out bytes.Buffer

	previous := slog.Default()

	t.Cleanup(func() { slog.SetDefault(previous) })

	require.NoError(t, logging.Setup(logging.Options{
		Level: slog.LevelInfo, Format: logging.FormatText, Writer: &logged,
		IsTerminal: func(io.Writer) bool { return false },
	}))

	root.SetOut(&out)
	root.SetErr(errorLogWriter{})
	root.SetArgs(args)

	code := sdk.ExitOK

	cmd, err := root.ExecuteC()
	if err != nil {
		code = reportError(cmd, err)
	}

	return unknownRun{code: code, logged: logged.String(), out: out.String()}
}

// newGroupTree returns a root with the group "grp" of "build" and "clean",
// and "nested" under it, a group of "show", after rejectUnknownSubcommands.
func newGroupTree() *cobra.Command {
	noop := func(*cobra.Command, []string) error { return nil }

	nested := &cobra.Command{Use: "nested", Short: "A nested group"}
	nested.AddCommand(&cobra.Command{Use: "show", RunE: noop})

	group := &cobra.Command{Use: "grp", Short: "A group"}
	group.AddCommand(
		&cobra.Command{Use: "build", RunE: noop},
		&cobra.Command{Use: "clean", RunE: noop},
		nested,
	)

	root := &cobra.Command{Use: "tt", RunE: groupRunE, TraverseChildren: true}
	root.AddCommand(group)

	rejectUnknownSubcommands(root)

	return root
}

// TestUnknownSubcommand checks that a group fails on a subcommand it does
// not have, with the commands meant and where the usage is, and prints its
// help when given none.
//
//nolint:paralleltest // Replaces slog.Default for the duration of each case.
func TestUnknownSubcommand(t *testing.T) {
	t.Run("a close name is suggested", func(t *testing.T) {
		const typed = "buidl" // codespell:ignore buidl

		got := runUnknown(t, newGroupTree(), "grp", typed)

		assert.Equal(t, sdk.ExitFailure, got.code)
		assert.Equal(t, `   ⨯ unknown command "`+typed+`" for "tt grp"
     Did you mean this?
         build
     Run 'tt grp --help' for usage.
`, got.logged)
		assert.Empty(t, got.out, "the usage is not printed")
	})

	t.Run("a far name is not", func(t *testing.T) {
		got := runUnknown(t, newGroupTree(), "grp", "frobnicate", "arg")

		assert.Equal(t, sdk.ExitFailure, got.code)
		assert.Equal(t, `   ⨯ unknown command "frobnicate" for "tt grp"
     Run 'tt grp --help' for usage.
`, got.logged)
	})

	t.Run("a nested group", func(t *testing.T) {
		got := runUnknown(t, newGroupTree(), "grp", "nested", "shw")

		assert.Equal(t, sdk.ExitFailure, got.code)
		assert.Contains(t, got.logged, `unknown command "shw" for "tt grp nested"`)
		assert.Contains(t, got.logged, "\n         show\n")
	})

	t.Run("a group alone prints its help", func(t *testing.T) {
		got := runUnknown(t, newGroupTree(), "grp")

		assert.Equal(t, sdk.ExitOK, got.code)
		assert.Empty(t, got.logged)
		assert.Contains(t, got.out, "A group")
		assert.Contains(t, got.out, "build")
	})

	t.Run("a subcommand runs", func(t *testing.T) {
		got := runUnknown(t, newGroupTree(), "grp", "clean")

		assert.Equal(t, sdk.ExitOK, got.code)
		assert.Empty(t, got.logged)
	})
}

// TestUnknownTopLevelCommand checks that the root names the command it does
// not have in one line, with where a command of tt 2 went, and does not
// print its usage.
//
//nolint:paralleltest // Replaces slog.Default for the duration of each case.
func TestUnknownTopLevelCommand(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		hint string
	}{
		{[]string{"start", "app:*"}, "tt 3 manages instances with tt instance start"},
		{[]string{"logrotate"}, "tt 3 manages instances with tt instance logrotate"},
		{[]string{"log", "-f"}, "tt 3 shows the logs of instances with tt instance logs"},
		{[]string{"play", "x.xlog"}, "with tt data play"},
		{[]string{"build"}, "tt 3 builds packages with tt package build"},
		{[]string{"pack", "tgz"}, "with tt package pack"},
		{[]string{"install", "tarantool"}, "with tt binary install"},
		{[]string{"init"}, "with tt new"},
		{[]string{"env"}, "tt env is gone in tt 3: source .env/activate.sh"},
		{[]string{"cfg", "dump"}, "tt cfg is gone in tt 3"},
		{
			[]string{"instnace", "start"}, // codespell:ignore instnace
			"Did you mean this?\n         instance\n",
		},
	} {
		t.Run(testCase.args[0], func(t *testing.T) {
			got := runUnknown(t, NewCmdRoot(), testCase.args...)

			assert.Equal(t, sdk.ExitFailure, got.code)
			assert.True(t, strings.HasPrefix(got.logged,
				`   ⨯ unknown command "`+testCase.args[0]+`" for "tt"`+"\n"), got.logged)
			assert.Contains(t, got.logged, testCase.hint)
			assert.True(t, strings.HasSuffix(got.logged, "     Run 'tt --help' for usage.\n"),
				got.logged)
			assert.Empty(t, got.out, "the usage is not printed")
		})
	}

	t.Run("a hint only for the root", func(t *testing.T) {
		got := runUnknown(t, newGroupTree(), "grp", "start")

		assert.NotContains(t, got.logged, "tt instance")
	})
}

// TestMovedCommandsAreGone checks that tt has none of the commands the hints
// are for: a hint is given only for a command tt does not run.
func TestMovedCommandsAreGone(t *testing.T) {
	t.Parallel()

	root := NewCmdRoot()

	for name := range movedCommands {
		for _, command := range root.Commands() {
			assert.False(t, command.Name() == name || command.HasAlias(name),
				"tt has %q, the hint for it is dead", name)
		}
	}
}

// TestRejectUnknownSubcommands checks which commands become groups: those
// with subcommands and no RunE of their own.
func TestRejectUnknownSubcommands(t *testing.T) {
	t.Parallel()

	ran := false
	own := &cobra.Command{Use: "own", RunE: func(*cobra.Command, []string) error {
		ran = true

		return nil
	}}
	own.AddCommand(&cobra.Command{Use: "sub", Run: func(*cobra.Command, []string) {}})

	leaf := &cobra.Command{Use: "topic", Short: "A help topic"}

	root := &cobra.Command{Use: "tt"}
	root.AddCommand(own, leaf)

	rejectUnknownSubcommands(root)

	assert.False(t, isGroup(own), "a command that runs keeps its RunE")
	require.NoError(t, own.RunE(own, nil))
	assert.True(t, ran)

	assert.False(t, isGroup(leaf), "a help topic stays one")
	assert.False(t, leaf.Runnable())
}

// TestGroupWithArgs checks that a command with subcommands that takes
// positional arguments of its own, by its RunE, its Args or its ValidArgs,
// is not made a group: an argument that names no subcommand reaches it and
// is not reported as an unknown command.
//
//nolint:paralleltest // Replaces slog.Default for the duration of each case.
func TestGroupWithArgs(t *testing.T) {
	var got []string

	runs := &cobra.Command{
		Use:  "runs [program]",
		Args: cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			got = args

			return nil
		},
	}
	runs.AddCommand(&cobra.Command{Use: "tarantool", Run: func(*cobra.Command, []string) {}})

	validated := &cobra.Command{Use: "validated", Args: cobra.ArbitraryArgs}
	validated.AddCommand(&cobra.Command{Use: "sub", Run: func(*cobra.Command, []string) {}})

	valid := &cobra.Command{Use: "valid", ValidArgs: []string{"one", "two"}}
	valid.AddCommand(&cobra.Command{Use: "sub", Run: func(*cobra.Command, []string) {}})

	root := &cobra.Command{Use: "tt", RunE: groupRunE, TraverseChildren: true}
	root.AddCommand(runs, validated, valid)

	rejectUnknownSubcommands(root)

	for _, command := range []*cobra.Command{runs, validated, valid} {
		assert.False(t, isGroup(command), command.Name())
	}

	run := runUnknown(t, root, "runs", "bash", "123")

	assert.Equal(t, sdk.ExitOK, run.code)
	assert.Empty(t, run.logged)
	assert.Equal(t, []string{"bash", "123"}, got)

	for _, name := range []string{"validated", "valid"} {
		run = runUnknown(t, root, name, "one")

		assert.Equal(t, sdk.ExitOK, run.code, name)
		assert.NotContains(t, run.logged, "unknown command", name)
	}
}
