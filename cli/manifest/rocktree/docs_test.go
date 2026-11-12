package rocktree_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// writeFiles writes files, by slash path relative to dir, into dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

// TestMirrorTree: the copy of a stage keeps the permission bits of each file,
// the umask not applied, and a symlink as the link it is; a file is copied
// into the parents it is given, and a missing stage copies nothing.
func TestMirrorTree(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeFiles(t, src, map[string]string{
		"bin/tool":        "#!/bin/sh\n",
		"lua/mylib/a.lua": "return {}\n",
	})
	require.NoError(t, os.Chmod(filepath.Join(src, "bin", "tool"), 0o775))
	require.NoError(t, os.Chmod(filepath.Join(src, "lua", "mylib", "a.lua"), 0o600))
	require.NoError(t, os.Symlink("a.lua", filepath.Join(src, "lua", "mylib", "b.lua")))

	dst := t.TempDir()
	require.NoError(t, rocktree.MirrorTree(src, dst))

	info, err := os.Stat(filepath.Join(dst, "bin", "tool"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o775), info.Mode().Perm())

	info, err = os.Stat(filepath.Join(dst, "lua", "mylib", "a.lua"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	link, err := os.Readlink(filepath.Join(dst, "lua", "mylib", "b.lua"))
	require.NoError(t, err)
	assert.Equal(t, "a.lua", link)

	file := filepath.Join(t.TempDir(), "doc", "tools", "tool")
	require.NoError(t, rocktree.MirrorTree(filepath.Join(src, "bin", "tool"), file))

	info, err = os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o775), info.Mode().Perm())

	empty := t.TempDir()
	require.NoError(t, rocktree.MirrorTree(filepath.Join(src, "missing"), empty))

	names, err := os.ReadDir(empty)
	require.NoError(t, err)
	assert.Empty(t, names)
}

// TestEntryFiles: an entry is the files it is or holds, through symlinks, a
// link that leads nowhere or round to a directory being walked left out; a
// missing entry is ErrNotExist. CopyEntry copies those files, modes
// normalized as pack copies them.
func TestEntryFiles(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	writeFiles(t, repo, map[string]string{
		"COPYING":                  "repository license\n",
		"shared/notice.md":         "shared notice\n",
		"mylib/licenses/vendor.md": "vendor\n",
	})

	dir := filepath.Join(repo, "mylib")
	links := map[string]string{
		"LICENSE":         filepath.Join("..", "COPYING"),
		"licenses/shared": filepath.Join("..", "..", "shared"),
		"licenses/gone":   "nowhere",
		"licenses/loop":   ".",
	}

	for link, target := range links {
		require.NoError(t, os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))))
	}

	require.NoError(t, os.Chmod(filepath.Join(repo, "COPYING"), 0o700))

	files, err := rocktree.EntryFiles(dir, "licenses")
	require.NoError(t, err)
	assert.Equal(t, []string{"licenses/shared/notice.md", "licenses/vendor.md"}, files)

	files, err = rocktree.EntryFiles(dir, "LICENSE")
	require.NoError(t, err)
	assert.Equal(t, []string{"LICENSE"}, files)

	_, err = rocktree.EntryFiles(dir, "NOTICE")
	require.ErrorIs(t, err, fs.ErrNotExist)

	doc := filepath.Join(t.TempDir(), "doc")
	require.NoError(t, rocktree.CopyEntry(dir, "LICENSE", doc))
	require.NoError(t, rocktree.CopyEntry(dir, "licenses", doc))

	info, err := os.Lstat(filepath.Join(doc, "LICENSE"))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm()&^umask(t))

	data, err := os.ReadFile(filepath.Join(doc, "licenses", "shared", "notice.md"))
	require.NoError(t, err)
	assert.Equal(t, "shared notice\n", string(data))

	require.ErrorIs(t, rocktree.CopyEntry(dir, "NOTICE", doc), fs.ErrNotExist)
}

// umask reads the umask of the process from the mode a fresh file gets.
func umask(t *testing.T) os.FileMode {
	t.Helper()

	path := filepath.Join(t.TempDir(), "probe")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o777)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)

	return 0o777 &^ info.Mode().Perm()
}

// TestWithDocs: the files of the rock are the stage and, in doc/, what fill
// puts there, apart from the stage, which stays as it is; an error of fill is
// the error of WithDocs, and the directory goes with it as with the cleanup.
func TestWithDocs(t *testing.T) {
	t.Parallel()

	stage := t.TempDir()
	writeFiles(t, stage, map[string]string{"lua/mylib/init.lua": "return {}\n"})

	temp := t.TempDir()

	dir, cleanup, err := rocktree.WithDocs(temp, stage, func(doc string) error {
		writeFiles(t, doc, map[string]string{"LICENSE": "text\n"})

		return nil
	})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "lua", "mylib", "init.lua"))
	assert.FileExists(t, filepath.Join(dir, rocktree.StageDoc, "LICENSE"))
	assert.NoDirExists(t, filepath.Join(stage, rocktree.StageDoc))

	cleanup()
	assert.NoDirExists(t, dir)

	failure := errors.New("no license")

	_, _, err = rocktree.WithDocs(temp, stage, func(string) error { return failure })
	require.ErrorIs(t, err, failure)

	left, err := os.ReadDir(temp)
	require.NoError(t, err)
	assert.Empty(t, left)
}
