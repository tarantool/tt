package core

import (
	"flag"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/aeon"
	"github.com/tarantool/tt/v3/cli/cmd/helpgolden"
)

// update rewrites the golden files of this package instead of comparing
// against them: go test ./core -run TestHelpGolden -update.
var update = flag.Bool("update", false, "rewrite golden files")

// helpGoldenPath is the golden file TestHelpGoldenThroughModules compares
// against.
const helpGoldenPath = "testdata/help.golden"

// TestHelpGoldenThroughModules pins what tt prints for help and completion:
// the --help of every command, the bash completion script and the root's
// completion candidates, for tt built from the modules tt's main package
// builds it from - constructors, mounting, injection, configuration. A
// change here is a change users see; regenerate the golden file with -update
// and review the diff.
//
//nolint:paralleltest // Replaces the process's root, logger, directory and os.Stdout.
func TestHelpGoldenThroughModules(t *testing.T) {
	// The test changes the working directory.
	golden, err := filepath.Abs(helpGoldenPath)
	require.NoError(t, err)

	restoreProcess(t)

	// Configuration is looked for from the working directory: there is none
	// in an empty one.
	t.Chdir(t.TempDir())
	t.Setenv("TT_CLI_MODULES_PATH", "")
	t.Setenv("TT_CLI_CFG", "")
	require.NoError(t, os.Unsetenv("TT_CLI_CFG"))

	modules := Modules{"builtin": Builtin, "aeon": aeon.New}

	newRoot := func(t *testing.T) *cobra.Command {
		t.Helper()

		root, err := build(nil, modules, &atomic.Bool{}, options{})
		require.NoError(t, err)

		return root
	}

	helpgolden.Compare(t, golden, helpgolden.Render(t, newRoot), *update)
}
