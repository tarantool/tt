package core

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/aeon"
	"github.com/tarantool/tt/modules/emmylua"
	"github.com/tarantool/tt/v3/cli/cmd/clidoc"
)

// clidocDir is where TestCLIReference writes the command reference of the
// documentation, a temporary directory when empty:
//
//	go test ./core -run TestCLIReference -args -clidoc DIR
//
// mage docs:reference runs it for doc/reference/commands.
var clidocDir = flag.String("clidoc", "", "write the command reference into DIR")

// TestCLIReference renders the command reference from tt as its main package
// builds it, the tree TestHelpGoldenThroughModules pins the help of.
//
//nolint:paralleltest // Replaces the process's root, logger and directory.
func TestCLIReference(t *testing.T) {
	dir := *clidocDir
	if dir == "" {
		dir = t.TempDir()
	}

	// The directory is given from where go test runs, before the test
	// changes it.
	dir, err := filepath.Abs(dir)
	require.NoError(t, err)

	restoreProcess(t)

	// Configuration is looked for from the working directory: there is none
	// in an empty one.
	t.Chdir(t.TempDir())
	t.Setenv("TT_CLI_MODULES_PATH", "")
	t.Setenv("TT_CLI_CFG", "")
	require.NoError(t, os.Unsetenv("TT_CLI_CFG"))

	root, err := build(nil,
		Modules{"builtin": Builtin, "aeon": aeon.New, "emmylua": emmylua.New},
		&atomic.Bool{}, options{})
	require.NoError(t, err)

	// cobra adds the help and completion commands when it executes.
	root.SetOut(io.Discard)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	pages, err := clidoc.Write(root, dir)
	require.NoError(t, err)
	require.Contains(t, pages, "instance.md")
	require.Contains(t, pages, "errors.md")
}
