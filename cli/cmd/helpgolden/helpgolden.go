// Package helpgolden renders what tt prints for help and completion, for
// tests that pin it against a golden file: the --help of every command, the
// bash completion script and the completions of the first word.
//
// The rendering takes the command tree from a function, so that every way of
// assembling tt can be compared against the same golden file.
package helpgolden

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewRoot builds the command tree the way tt does before it runs a command,
// with no external modules, and makes it the process's root. It is called
// once per command line rendered.
type NewRoot func(t *testing.T) *cobra.Command

// command is a command of the tree as Render records it.
type command struct {
	path        []string // Names from the root down, the root excluded.
	hidden      bool     // Hidden from help and completion.
	noFlagParse bool     // Flag parsing disabled: --help is the command's argument.
}

// Render renders the help of every command of the tree newRoot builds, the
// bash completion script and the completion candidates for the first word.
func Render(t *testing.T, newRoot NewRoot) string {
	t.Helper()

	var golden strings.Builder

	golden.WriteString("# The help of every tt command, the bash completion script and the\n" +
		"# completions of the first word. Regenerate with:\n" +
		"#   go test ./cli/cmd -run TestHelpGolden -update\n")

	section := func(title, body string) {
		golden.WriteString("\n=== " + title + "\n")
		golden.WriteString(body)

		if !strings.HasSuffix(body, "\n") {
			golden.WriteString("\n=== (no newline at end)\n")
		}
	}

	commands := walk(t, newRoot)
	for _, command := range commands {
		title := strings.Join(append([]string{"tt"}, command.path...), " ")

		var args []string

		if command.noFlagParse {
			// With flag parsing disabled --help would reach the command as an
			// argument - tt rocks would hand it to LuaRocks - so the help is
			// rendered the other way tt offers.
			args = append([]string{"help"}, command.path...)

			title += " (via tt help: flag parsing disabled)"
		} else {
			args = append(append([]string{}, command.path...), "--help")

			title += " --help"
		}

		if command.hidden {
			title += " [hidden]"
		}

		section(title, run(t, newRoot, args...))
	}

	require.Greater(t, len(commands), 50, "the walk must reach the whole tree")

	section("tt completion bash", run(t, newRoot, "completion", "bash"))
	section(`tt __complete ""`, run(t, newRoot, cobra.ShellCompRequestCmd, ""))

	return golden.String()
}

// RenderHelp renders the --help of the commands at paths, each given by the
// names from the root down - none for the root - under header, each of its
// lines a comment.
func RenderHelp(t *testing.T, newRoot NewRoot, header string, paths ...[]string) string {
	t.Helper()

	var golden strings.Builder

	for line := range strings.SplitSeq(header, "\n") {
		golden.WriteString("# " + line + "\n")
	}

	for _, path := range paths {
		title := strings.Join(append([]string{"tt"}, path...), " ") + " --help"
		body := run(t, newRoot, append(append([]string{}, path...), "--help")...)

		golden.WriteString("\n=== " + title + "\n" + body)
	}

	return golden.String()
}

// Compare compares got with the golden file at path, or rewrites the file
// with got when update is set.
func Compare(t *testing.T, path, got string, update bool) {
	t.Helper()

	if update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644)) //nolint:gosec

		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "run with -update to create the golden file")
	assert.Equal(t, string(want), got, "help output changed; rerun with -update and review")
}

// run executes args on a fresh tree and returns what it wrote to its output
// stream and to os.Stdout, which some commands write to directly, followed
// by what it wrote to its error stream, if anything.
func run(t *testing.T, newRoot NewRoot, args ...string) string {
	t.Helper()

	root := newRoot(t)

	var out, errOut bytes.Buffer

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)

	previous := os.Stdout

	os.Stdout = stdout

	runErr := root.Execute()

	os.Stdout = previous

	require.NoError(t, runErr, "tt %q", args)

	direct, err := os.ReadFile(stdout.Name())
	require.NoError(t, err)
	require.NoError(t, stdout.Close())

	result := out.String() + string(direct)
	if errOut.Len() != 0 {
		result += "--- stderr\n" + errOut.String()
	}

	return result
}

// walk lists every command of the tree - hidden ones and the ones cobra
// adds when it executes (help, __complete) included - depth first in cobra's
// order.
func walk(t *testing.T, newRoot NewRoot) []command {
	t.Helper()

	root := newRoot(t)
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	var commands []command

	var visit func(cmd *cobra.Command, path []string)

	visit = func(cmd *cobra.Command, path []string) {
		commands = append(commands, command{
			path:        path,
			hidden:      cmd.Hidden,
			noFlagParse: cmd.DisableFlagParsing,
		})

		for _, sub := range cmd.Commands() {
			visit(sub, append(append([]string{}, path...), sub.Name()))
		}
	}
	visit(root, nil)

	return commands
}
