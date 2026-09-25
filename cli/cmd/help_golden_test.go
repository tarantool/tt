package cmd

import (
	"flag"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/v3/cli/cmd/helpgolden"
	"github.com/tarantool/tt/v3/cli/modules"
)

// update rewrites the golden files of this package instead of comparing
// against them: go test ./cli/cmd -run TestHelpGolden -update.
var update = flag.Bool("update", false, "rewrite golden files")

// helpGoldenPath is the golden file TestHelpGolden compares against.
const helpGoldenPath = "testdata/help.golden"

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

// TestHelpGolden pins what tt prints for help and completion: the --help of
// every command, the bash completion script and the root's completion
// candidates. A change here is a change users see; regenerate the golden file
// with -update and review the diff.
//
//nolint:paralleltest // Replaces the package root and os.Stdout.
func TestHelpGolden(t *testing.T) {
	helpgolden.Compare(t, helpGoldenPath, helpgolden.Render(t, newGoldenRoot), *update)
}
