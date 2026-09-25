package core

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmd/helpgolden"
)

// TestHelpGoldenThroughModules checks that tt built from the builtin module -
// constructors, mounting, injection, configuration - prints the same help
// and completion as the command tree cli/cmd pins.
//
//nolint:paralleltest // Replaces the process's root, logger, directory and os.Stdout.
func TestHelpGoldenThroughModules(t *testing.T) {
	golden, err := filepath.Abs(filepath.Join("..", "cli", "cmd", "testdata", "help.golden"))
	require.NoError(t, err)

	restoreProcess(t)

	// Configuration is looked for from the working directory: there is none
	// in an empty one, as for the tree cli/cmd pins.
	t.Chdir(t.TempDir())
	t.Setenv("TT_CLI_MODULES_PATH", "")
	t.Setenv("TT_CLI_CFG", "")
	require.NoError(t, os.Unsetenv("TT_CLI_CFG"))

	newRoot := func(t *testing.T) *cobra.Command {
		t.Helper()

		root, err := build(nil, Modules{"builtin": Builtin}, &atomic.Bool{})
		require.NoError(t, err)

		return root
	}

	helpgolden.Compare(t, golden, helpgolden.Render(t, newRoot), false)
}
