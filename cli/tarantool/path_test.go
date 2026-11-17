package tarantool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

// pathList joins dirs into a PATH value.
func pathList(dirs ...string) string {
	return strings.Join(dirs, string(os.PathListSeparator))
}

// TestPrependPathProjectBin: the tarantool of a project puts the directory
// of its link, .tarantool/bin, first in PATH, not the version directory the
// link selects, and a second call puts nothing more there.
func TestPrependPathProjectBin(t *testing.T) {
	inherited := t.TempDir()
	t.Setenv("PATH", inherited)

	root := scope.ProjectRoot(t.TempDir())
	target := install(t, root, ce("3.9.0"))
	activate(t, root, target)

	bin := filepath.Join(root, binary.BinDirName)

	require.NoError(t, tarantool.PrependPath(target, root))
	assert.Equal(t, pathList(bin, inherited), os.Getenv("PATH"))

	require.NoError(t, tarantool.PrependPath(target, root))
	assert.Equal(t, pathList(bin, inherited), os.Getenv("PATH"), "PATH is extended once")
}

// TestPrependPathOtherTarantool: a tarantool PATH finds that is not the one
// of the build does not keep its place.
func TestPrependPathOtherTarantool(t *testing.T) {
	other := filepath.Dir(pathTarantool(t))

	root := scope.ProjectRoot(t.TempDir())
	target := install(t, root, ce("3.9.0"))
	activate(t, root, target)

	require.NoError(t, tarantool.PrependPath(target, root))
	assert.Equal(t, pathList(filepath.Join(root, binary.BinDirName), other), os.Getenv("PATH"))
}

// TestPrependPathExecutableDir: with no link to the executable in
// <prefix>/bin, as in a flat Enterprise SDK, the directory of the executable
// goes first; with an empty PATH it is the whole of PATH.
func TestPrependPathExecutableDir(t *testing.T) {
	inherited := t.TempDir()
	t.Setenv("PATH", inherited)

	sdk := t.TempDir()
	exe := filepath.Join(sdk, binary.ProgramCe.Exec())
	writeExecutable(t, exe)

	require.NoError(t, tarantool.PrependPath(exe, sdk))
	assert.Equal(t, pathList(sdk, inherited), os.Getenv("PATH"))

	t.Setenv("PATH", "")

	require.NoError(t, tarantool.PrependPath(exe, ""))
	assert.Equal(t, sdk, os.Getenv("PATH"))
}

// TestPrependPathLeavesPath: PATH is left alone when it finds the tarantool
// already, the active one of a scope through <root>/bin say, and when no
// directory would make it find the tarantool.
func TestPrependPathLeavesPath(t *testing.T) {
	root := t.TempDir()
	target := install(t, root, ce("3.9.0"))
	activate(t, root, target)

	renamed := filepath.Join(t.TempDir(), "tarantool-3.9.0")
	writeExecutable(t, renamed)

	for name, executable := range map[string]string{
		"no tarantool":    "",
		"on PATH":         target,
		"relative":        binary.ProgramCe.Exec(),
		"not there":       filepath.Join(t.TempDir(), binary.ProgramCe.Exec()),
		"named otherwise": renamed,
	} {
		t.Run(name, func(t *testing.T) {
			path := pathList(t.TempDir(), filepath.Join(root, binary.BinDirName))
			t.Setenv("PATH", path)

			require.NoError(t, tarantool.PrependPath(executable, root))
			assert.Equal(t, path, os.Getenv("PATH"))
		})
	}
}
