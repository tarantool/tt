package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapHelp(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{
			name:  "short lines are kept verbatim",
			in:    "USAGE\n  tt  x   y\n",
			width: 20,
			want:  "USAGE\n  tt  x   y\n",
		},
		{
			name:  "prose continues at its indentation",
			in:    "  one two three four five",
			width: 12,
			want:  "  one two\n  three four\n  five",
		},
		{
			name:  "flag continues under its description",
			in:    "  -o, --format string   output format: table or json",
			width: 50,
			want: "  -o, --format string   output format: table or\n" +
				"                        json",
		},
		{
			name:  "list item continues under its text",
			in:    "* verify_host - set off verification\n  - key - a target key",
			width: 20,
			want:  "* verify_host - set\n  off verification\n  - key - a target\n    key",
		},
		{
			name:  "wide head falls back to the line indentation",
			in:    "  a-very-long-flag-name  one two three",
			width: 30,
			want:  "  a-very-long-flag-name one\n  two three",
		},
		{
			name:  "a word wider than width is kept whole",
			in:    "see https://example.com/a/very/long/path now",
			width: 10,
			want:  "see\nhttps://example.com/a/very/long/path\nnow",
		},
		{
			name:  "escape sequences take no columns",
			in:    "\x1b[1mUSAGE\x1b[0m",
			width: 5,
			want:  "\x1b[1mUSAGE\x1b[0m",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, wrapHelp(tc.in, tc.width))
		})
	}
}

// TestErrorsHelpTopic checks that tt help errors describes every exit code tt
// commands use, and that it is listed as a help topic rather than as a
// command, since there is nothing to run.
func TestErrorsHelpTopic(t *testing.T) {
	root := NewCmdRoot()
	configureHelpCommand(root, nil)

	topic, _, err := root.Find([]string{"errors"})
	require.NoError(t, err)
	require.Equal(t, "errors", topic.Name())
	assert.True(t, topic.IsAdditionalHelpTopicCommand())

	var out strings.Builder

	topic.SetOut(&out)
	require.NoError(t, topic.Help())

	for _, code := range []string{"  0  ", "  1  ", "  2  ", "  3  "} {
		assert.Contains(t, out.String(), code)
	}

	var rootOut strings.Builder

	root.SetOut(&rootOut)
	require.NoError(t, root.Help())

	help := rootOut.String()
	topics := strings.Index(help, "HELP TOPICS")
	require.NotEqual(t, -1, topics, "the root help must list help topics")
	assert.Contains(t, help[topics:], "errors")
	assert.NotContains(t, help[:topics], "\n  errors ", "a topic is not a command")
}

// TestHelpColor checks that the help is coloured on a terminal only, and
// there not with NO_COLOR set to a value or TERM=dumb.
func TestHelpColor(t *testing.T) {
	t.Parallel()

	env := func(vars map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			value, ok := vars[key]

			return value, ok
		}
	}

	assert.True(t, colorsHelp(true, env(nil)))
	assert.True(t, colorsHelp(true, env(map[string]string{"TERM": "xterm", "NO_COLOR": ""})))
	assert.False(t, colorsHelp(false, env(nil)))
	assert.False(t, colorsHelp(true, env(map[string]string{"NO_COLOR": "1"})))
	assert.False(t, colorsHelp(true, env(map[string]string{"TERM": "dumb"})))

	// A buffer and a pipe are not terminals.
	assert.False(t, isTerminal(&strings.Builder{}))

	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	assert.False(t, isTerminal(writer))

	// So the help written to them has no escape sequences.
	root := NewCmdRoot()
	configureHelpCommand(root, []ExternalCommand{{Name: "ext", Help: "An external command"}})

	var out strings.Builder

	root.SetOut(&out)
	require.NoError(t, root.Help())
	assert.NotContains(t, out.String(), "\x1b")
	assert.Contains(t, out.String(), "\nUSAGE\n")
	assert.Contains(t, out.String(), "\nEXTERNAL COMMANDS\n  ext\tAn external command\n\nFLAGS\n")
}

func TestHelpFitsWidth(t *testing.T) {
	root := NewCmdRoot()
	configureHelpCommand(root, nil)

	checked := 0

	var walk func(cmd *cobra.Command)

	walk = func(cmd *cobra.Command) {
		var out strings.Builder

		cmd.SetOut(&out)
		require.NoError(t, cmd.Help())
		require.NotEmpty(t, out.String(), cmd.CommandPath())

		for line := range strings.SplitSeq(out.String(), "\n") {
			assert.LessOrEqual(t, textWidth(line), helpWidth, "%s: %s", cmd.CommandPath(), line)
		}

		checked++

		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)
	require.Greater(t, checked, 50)
}
