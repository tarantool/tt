package srctree_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/internal/srctree"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// walked lists what a walk of the directory at domain under root visits.
func walked(t *testing.T, root string, domain []string, skip srctree.Skip) []string {
	t.Helper()

	inherited, err := srctree.Ancestors(root, domain)
	require.NoError(t, err)

	var visited []string

	dir := filepath.Join(append([]string{root}, domain...)...)

	err = srctree.Walk(dir, domain, inherited, skip, func(
		_ string, path []string, _ fs.DirEntry,
	) error {
		visited = append(visited, strings.Join(path, "/"))

		return nil
	})
	require.NoError(t, err)

	return visited
}

func TestWalk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".gitignore":          "*.o\n# a comment\n\nsub/generated/\n",
		"a.lua":               "",
		"a.o":                 "",
		"sub/.gitignore":      "local.txt\n",
		"sub/b.lua":           "",
		"sub/local.txt":       "",
		"sub/generated/c.lua": "",
		"sub/deep/d.lua":      "",
		"sub/deep/local.txt":  "",
		"skipped/e.lua":       "",
	})
	require.NoError(t, os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link")))

	skip := func(path []string, _ fs.DirEntry) bool { return path[len(path)-1] == "skipped" }

	assert.Equal(t, []string{
		".gitignore", "a.lua", "link",
		"sub/.gitignore", "sub/b.lua", "sub/deep/d.lua",
	}, walked(t, root, nil, skip), "in name order, the rules of each directory for its subtree")

	assert.Equal(t, []string{"sub/.gitignore", "sub/b.lua", "sub/deep/d.lua"},
		walked(t, root, []string{"sub"}, nil), "the rules above a subdirectory hold for it")
}
