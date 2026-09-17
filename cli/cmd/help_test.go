package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/modules"
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

func TestHelpFitsWidth(t *testing.T) {
	root := NewCmdRoot()
	configureHelpCommand(root, &modules.ModulesInfo{})

	checked := 0
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		var out strings.Builder
		cmd.SetOut(&out)
		require.NoError(t, cmd.Help())
		require.NotEmpty(t, out.String(), cmd.CommandPath())
		for _, line := range strings.Split(out.String(), "\n") {
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
