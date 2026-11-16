package run_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/run"
)

// rootLuaPath is the LUA_PATH entries of root.
func rootLuaPath(root string) string {
	return filepath.Join(root, "?.lua") + ";" + filepath.Join(root, "?", "init.lua")
}

// moduleExt is the extension Tarantool loads a C module with here.
func moduleExt() string {
	if runtime.GOOS == "darwin" {
		return ".dylib"
	}

	return ".so"
}

// In the root, Tarantool finds the sources of the project from its working
// directory, so nothing is composed.
func TestSearchPathEnvLeavesARunInTheRootAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	environ := []string{"HOME=/home/u", "LUA_PATH=/x/?.lua"}

	assert.Equal(t, environ, run.SearchPathEnv(environ, root, root))
	assert.Equal(t, environ, run.SearchPathEnv(environ, root, root+string(filepath.Separator)))
}

// A root reached through a symlink is the root all the same.
func TestSearchPathEnvSeesTheRootThroughASymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(root, link))

	environ := []string{"HOME=/home/u"}

	assert.Equal(t, environ, run.SearchPathEnv(environ, root, link))
}

// Below the root, the root leads both paths, and unset paths keep the
// defaults through ";;".
func TestSearchPathEnvPutsTheRootAheadBelowIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	environ := []string{"HOME=/home/u"}

	env := run.SearchPathEnv(environ, root, filepath.Join(root, "sub"))

	assert.Equal(t, []string{
		"HOME=/home/u",
		"LUA_PATH=" + rootLuaPath(root) + ";;",
		"LUA_CPATH=" + filepath.Join(root, "?"+moduleExt()) + ";;",
	}, env)
	assert.Equal(t, []string{"HOME=/home/u"}, environ, "the environment given is not changed")
}

// A path the user set is kept, after the root.
func TestSearchPathEnvKeepsTheUsersPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	environ := []string{"LUA_PATH=/x/?.lua", "LUA_CPATH=/x/?.so;;"}

	env := run.SearchPathEnv(environ, root, filepath.Join(root, "sub"))

	assert.Equal(t, []string{
		"LUA_PATH=" + rootLuaPath(root) + ";/x/?.lua",
		"LUA_CPATH=" + filepath.Join(root, "?"+moduleExt()) + ";/x/?.so;;",
	}, env)
}

// A root no path entry can name is not put into one.
func TestSearchPathEnvSkipsARootAPathCannotName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"a;b", "a?b"} {
		root := filepath.Join(t.TempDir(), name)
		environ := []string{"HOME=/home/u"}

		assert.Equal(t, environ, run.SearchPathEnv(environ, root, filepath.Join(root, "sub")), name)
	}
}
