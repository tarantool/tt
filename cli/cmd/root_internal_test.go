package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/cli/logging"
)

// TestRootFlags checks that the root parses its flags up to the command: a
// flag of the same name after it belongs to the command.
func TestRootFlags(t *testing.T) {
	previous := logFormat

	t.Cleanup(func() { logFormat = previous })

	rootCmd = NewCmdRoot()
	_ = rootCmd.ParseFlags([]string{"--log-format", "json", "rocks", "--log-format", "text"})

	assert.Equal(t, logging.FormatJSON, logFormat)
}

// NewCmdRoot creates a new root command with every builtin and injected
// command.
func NewCmdRoot() *cobra.Command {
	root := newRootCmd()
	root.AddCommand(BuiltinCommands()...)

	err := InjectCommands(root)
	if err != nil {
		panic(err.Error())
	}

	root.SetErr(errorLogWriter{})

	return root
}
