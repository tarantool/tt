package rocktree_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// openTree opens the tree at root.
func openTree(t *testing.T, root string) *tree.Tree {
	t.Helper()

	rocksTree, err := tree.Open(luarocks.Config{Tree: root})
	require.NoError(t, err)

	return rocksTree
}

// repair runs Repair over the tree at root and returns what it logged.
func repair(t *testing.T, root string) []string {
	t.Helper()

	var logged []string

	err := rocktree.Repair(t.Context(), openTree(t, root), func(msg string) {
		logged = append(logged, msg)
	})
	require.NoError(t, err)

	return logged
}

// TestRepairLeavesATreeInStep pins that a tree whose manifest says what is on
// disk is not rebuilt.
func TestRepairLeavesATreeInStep(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	treetest.Install(t, root, treetest.Rock{Name: "checks", Version: "3.1.0"})

	assert.Empty(t, repair(t, root))
	assert.Empty(t, repair(t, t.TempDir()), "an empty tree")
}

// TestRepairDropsAnEntryWithoutItsRock covers what an older tt's uninstall
// left: the rock dir removed by hand, its entry still in the manifest.
func TestRepairDropsAnEntryWithoutItsRock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	treetest.Install(t, root,
		treetest.Rock{Name: "checks", Version: "3.1.0"},
		treetest.Rock{Name: "metrics", Version: "1.0.0"})

	require.NoError(t, os.RemoveAll(filepath.Join(root, "share", "tarantool", "rocks", "metrics")))

	logged := repair(t, root)
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "rebuilding it from the rocks on disk")

	assert.Empty(t, treetest.Versions(t, root, "metrics"))
	assert.Equal(t, []string{"3.1.0-1"}, treetest.Versions(t, root, "checks"))
	assert.Empty(t, repair(t, root), "a repaired tree is in step")
}

// TestRepairListsARockTheManifestDoesNot covers what an older tt's install
// left: the rocks of an archive extracted into the tree, without their
// entries in its manifest.
func TestRepairListsARockTheManifestDoesNot(t *testing.T) {
	t.Parallel()

	archive := t.TempDir()
	treetest.Install(t, archive, treetest.Rock{Name: "metrics", Version: "1.0.0"})

	root := t.TempDir()
	treetest.Install(t, root, treetest.Rock{Name: "checks", Version: "3.1.0"})

	// Extract the archive's rock over the tree, manifest aside.
	copyTree(t, archive, root, func(rel string) bool {
		return rel == "share/tarantool/rocks/manifest" || strings.HasSuffix(rel, ".lock")
	})

	require.Empty(t, treetest.Versions(t, root, "metrics"))

	require.Len(t, repair(t, root), 1)
	assert.Equal(t, []string{"1.0.0-1"}, treetest.Versions(t, root, "metrics"))
	assert.Equal(t, []string{"3.1.0-1"}, treetest.Versions(t, root, "checks"))
}

// TestRepairRewritesAManifestThatDoesNotParse covers a manifest no reader can
// take.
func TestRepairRewritesAManifestThatDoesNotParse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	treetest.Install(t, root, treetest.Rock{Name: "checks", Version: "3.1.0"})

	require.NoError(t, os.WriteFile(filepath.Join(root, "share", "tarantool", "rocks", "manifest"),
		[]byte("this is not lua"), 0o600))

	require.Len(t, repair(t, root), 1)
	assert.Equal(t, []string{"3.1.0-1"}, treetest.Versions(t, root, "checks"))
}

// copyTree copies the regular files of src over dst, but those skip names by
// their slash path.
func copyTree(t *testing.T, src, dst string, skip func(string) bool) {
	t.Helper()

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil || skip(filepath.ToSlash(rel)) {
			return err
		}

		data, err := os.ReadFile(path) //nolint:gosec // The test's own temp dir.
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		err = os.MkdirAll(filepath.Dir(target), 0o750)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, info.Mode().Perm())
	})
	require.NoError(t, err)
}
