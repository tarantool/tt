package cmd

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/modules"
)

// update rewrites the golden files of this package instead of comparing
// against them: go test ./cli/cmd -run TestHelpGolden -update.
var update = flag.Bool("update", false, "rewrite golden files")

// helpGoldenPath is the golden file TestHelpGolden compares against.
const helpGoldenPath = "testdata/help.golden"

// goldenCommand is a command of the tree as TestHelpGolden records it.
type goldenCommand struct {
	path        []string // Names from the root down, the root excluded.
	hidden      bool     // Hidden from help and completion.
	noFlagParse bool     // Flag parsing disabled: --help is the command's argument.
}

// newGoldenRoot builds the command tree the way tt does before it runs a
// command, with no external modules, and makes it the package's root.
func newGoldenRoot(t *testing.T) *cobra.Command {
	t.Helper()

	previous := rootCmd

	t.Cleanup(func() { rootCmd = previous })

	root := NewCmdRoot()
	configureHelpCommand(root, &modules.ModulesInfo{})

	rootCmd = root

	return root
}

// runGolden executes args on a fresh tree and returns what it wrote to its
// output stream and to os.Stdout, which some commands write to directly,
// followed by what it wrote to its error stream, if anything.
func runGolden(t *testing.T, args ...string) string {
	t.Helper()

	root := newGoldenRoot(t)

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

// goldenCommands lists every command of the tree - hidden ones and the ones
// cobra adds when it executes (help, __complete) included - depth first in
// cobra's order.
func goldenCommands(t *testing.T) []goldenCommand {
	t.Helper()

	root := newGoldenRoot(t)
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	var commands []goldenCommand

	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		commands = append(commands, goldenCommand{
			path:        path,
			hidden:      cmd.Hidden,
			noFlagParse: cmd.DisableFlagParsing,
		})

		for _, sub := range cmd.Commands() {
			walk(sub, append(append([]string{}, path...), sub.Name()))
		}
	}
	walk(root, nil)

	return commands
}

// renderHelpGolden renders the help of every command, the bash completion
// script and the completion candidates for the first word.
func renderHelpGolden(t *testing.T) string {
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

	commands := goldenCommands(t)
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

		section(title, runGolden(t, args...))
	}

	require.Greater(t, len(commands), 50, "the walk must reach the whole tree")

	section("tt completion bash", runGolden(t, "completion", "bash"))
	section(`tt __complete ""`, runGolden(t, cobra.ShellCompRequestCmd, ""))

	return golden.String()
}

// TestHelpGolden pins what tt prints for help and completion: the --help of
// every command, the bash completion script and the root's completion
// candidates. A change here is a change users see; regenerate the golden file
// with -update and review the diff.
//
//nolint:paralleltest // Replaces the package root and os.Stdout.
func TestHelpGolden(t *testing.T) {
	got := renderHelpGolden(t)

	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(helpGoldenPath), 0o755))
		require.NoError(t, os.WriteFile(helpGoldenPath, []byte(got), 0o644)) //nolint:gosec

		return
	}

	want, err := os.ReadFile(helpGoldenPath)
	require.NoError(t, err, "run with -update to create the golden file")
	assert.Equal(t, string(want), got, "help output changed; rerun with -update and review")
}
