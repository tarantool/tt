package clidoc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmd/clidoc"
)

func TestMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "paragraphs escaped",
			text: "Run <APP>:* with --scope_name.\n\nSecond [one].",
			want: "Run &lt;APP&gt;:\\* with --scope\\_name.\n\nSecond \\[one\\].\n\n",
		},
		{
			name: "markup at the start of a line",
			text: "# not a heading\n1. not a list\n+ nor this",
			want: "\\# not a heading\n1\\. not a list\n\\+ nor this\n\n",
		},
		{
			name: "list right after its introduction",
			text: "Arguments:\n* key - a key\n  continued.\n* name - a name.\nAfter.",
			want: "Arguments:\n\n- key - a key continued.\n- name - a name.\n\nAfter.\n\n",
		},
		{
			name: "indented lines keep their layout",
			text: "Modes:\n  full  - a new chain\n  incremental  - on top",
			want: "Modes:\n\n```text\nfull  - a new chain\nincremental  - on top\n```\n\n",
		},
		{
			name: "indented lines go on after a blank line",
			text: "Sample:\n  [core]\n  tt = \"v3\"\n\n  [build]\n\nAfter.",
			want: "Sample:\n\n```text\n[core]\ntt = \"v3\"\n\n[build]\n```\n\nAfter.\n\n",
		},
		{
			name: "long indented line wrapped under its text",
			text: "  1  one two three four five six seven eight nine ten eleven twelve " +
				"thirteen fourteen",
			want: "```text\n1  one two three four five six seven eight nine ten eleven twelve " +
				"thirteen\n   fourteen\n```\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, clidoc.Markdown(test.text))
		})
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "tt", Short: "Tarantool CLI"}
	root.Flags().BoolP("verbose", "V", false, "Verbose output")

	app := &cobra.Command{
		Use:     "app",
		Short:   "Manage apps",
		Aliases: []string{"a"},
	}
	start := &cobra.Command{
		Use:     "start [APP]",
		Short:   "Start an app",
		Long:    "Start the app.",
		Example: "  $ tt app start",
		Run:     func(*cobra.Command, []string) {},
	}
	start.Flags().String("scope", "project", "Scope of the app")
	start.Flags().Bool("secret", false, "")
	require.NoError(t, start.Flags().MarkHidden("secret"))

	hidden := &cobra.Command{Use: "internal", Hidden: true, Run: func(*cobra.Command, []string) {}}
	topic := &cobra.Command{Use: "errors", Short: "Exit codes", Long: "  0  Success."}

	app.AddCommand(start)
	root.AddCommand(app, hidden, topic)

	dir := filepath.Join(t.TempDir(), "commands")

	names, err := clidoc.Write(root, dir)
	require.NoError(t, err)
	assert.Equal(t, []string{clidoc.IndexPage, "app.md", "errors.md"}, names)

	index, err := os.ReadFile(filepath.Join(dir, clidoc.IndexPage))
	require.NoError(t, err)
	assert.Contains(t, string(index), "- [`tt app`](app.md) - Manage apps\n")
	assert.Contains(t, string(index), "- [`tt help errors`](errors.md) - Exit codes\n")
	assert.Contains(t, string(index), "- `-V`, `--verbose` - Verbose output\n")
	assert.NotContains(t, string(index), "tt internal")

	page, err := os.ReadFile(filepath.Join(dir, "app.md"))
	require.NoError(t, err)
	assert.Contains(t, string(page), "# tt app\n")
	assert.Contains(t, string(page), "Aliases: `a`.\n")
	assert.Contains(t, string(page), "- [`tt app start`](#tt-app-start) - Start an app\n")
	assert.Contains(t, string(page), "## tt app start\n\nStart the app.\n")
	assert.Contains(t, string(page), "```text\ntt app start [APP] [flags]\n```\n")
	assert.Contains(t, string(page), "- `--scope string` - Scope of the app (default `project`)\n")
	assert.Contains(t, string(page), "```console\n$ tt app start\n```\n")
	assert.NotContains(t, string(page), "secret")

	_, err = clidoc.Write(app, dir)
	require.Error(t, err)
}
