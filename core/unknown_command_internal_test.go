package core

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/logging"
)

// groupsOf returns the commands under command that have subcommands.
func groupsOf(command *cobra.Command) []*cobra.Command {
	var groups []*cobra.Command

	for _, sub := range command.Commands() {
		if sub.HasSubCommands() {
			groups = append(groups, sub)
		}

		groups = append(groups, groupsOf(sub)...)
	}

	return groups
}

// TestUnknownSubcommandOfEveryGroup runs every command group of tt as its
// main package builds it, the modules' included, with a subcommand the group
// does not have and with a typo of one it has: each fails with exit code 1,
// names the group, suggests what the typo meant and prints no help.
//
//nolint:paralleltest // Replaces the process's root, logger and directory.
func TestUnknownSubcommandOfEveryGroup(t *testing.T) {
	restoreProcess(t)

	t.Chdir(t.TempDir())
	t.Setenv("TT_CLI_MODULES_PATH", "")
	t.Setenv("TT_CLI_CFG", "")
	require.NoError(t, os.Unsetenv("TT_CLI_CFG"))

	root, err := build(nil, mainModules(), &atomic.Bool{}, options{})
	require.NoError(t, err)

	var logged, out bytes.Buffer

	require.NoError(t, logging.Setup(logging.Options{
		Level: slog.LevelInfo, Format: logging.FormatText, Writer: &logged,
		IsTerminal: func(io.Writer) bool { return false },
	}))

	root.SetOut(&out)

	groups := groupsOf(root)
	require.NotEmpty(t, groups)

	for _, group := range groups {
		path := group.CommandPath()
		words := strings.Fields(path)[1:]

		var first string

		for _, sub := range group.Commands() {
			if sub.IsAvailableCommand() && len(sub.Name()) > 3 {
				first = sub.Name()

				break
			}
		}

		cases := map[string]string{"nosuchcommand": ""}
		if first != "" {
			cases[first+"x"] = first
		}

		for typed, meant := range cases {
			logged.Reset()
			out.Reset()
			root.SetArgs(append(append([]string{}, words...), typed))

			code := cmd.Run()

			assert.Equal(t, 1, code, "%s %s", path, typed)
			assert.Contains(t, logged.String(),
				`unknown command "`+typed+`" for "`+path+`"`, path)
			assert.Contains(t, logged.String(), "Run '"+path+" --help' for usage.", path)
			assert.Empty(t, out.String(), "%s %s prints no help", path, typed)

			if meant != "" {
				assert.Contains(t, logged.String(), "Did you mean this?", path)
				assert.Contains(t, logged.String(), "    "+meant+"\n", path)
			}
		}
	}
}
