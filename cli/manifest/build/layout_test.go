package build

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// writeFile creates path (with parents) and writes content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// seedComponentTree lays down a representative component tree under root and
// returns root.
func seedComponentTree(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "init.lua"), "-- init")
	writeFile(t, filepath.Join(root, "lib", "foo.lua"), "-- foo")
	writeFile(t, filepath.Join(root, "prebuilt.so"), "so")
	writeFile(t, filepath.Join(root, "README.md"), "readme")
	writeFile(t, filepath.Join(root, "fast_hash.c"), "int main(){}")
	writeFile(t, filepath.Join(root, "test", "spec.lua"), "-- test")
	writeFile(t, filepath.Join(root, ".hidden.lua"), "-- hidden")
	writeFile(t, filepath.Join(root, manifestFileName), "manifest")
}

// relSet returns the tree-relative slash paths of files present under tree.
func relSet(t *testing.T, tree string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.Walk(tree, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, relErr := filepath.Rel(tree, path)
			require.NoError(t, relErr)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	}))
	sort.Strings(out)
	return out
}

func TestLayoutComponent_defaultNamespaceSplitsByExt(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	tree := filepath.Join(project, ".rocks")
	seedComponentTree(t, project)

	written, err := layoutComponent(tree, "my-app", manifest.Component{Path: "."}, project)
	require.NoError(t, err)

	// .lua goes to share, .so to lib, both under the my-app namespace; the
	// README, C source, test/, hidden and manifest files are all filtered out.
	assert.ElementsMatch(t, []string{
		"lib/tarantool/my-app/prebuilt.so",
		"share/tarantool/my-app/init.lua",
		"share/tarantool/my-app/lib/foo.lua",
	}, relSet(t, tree))

	// Returned destinations are the absolute paths written.
	assert.Contains(t, written, filepath.Join(tree, "share/tarantool/my-app/init.lua"))
	assert.Len(t, written, 3)
}

func TestLayoutComponent_emptyNamespaceIsFlat(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	tree := filepath.Join(project, ".rocks")
	seedComponentTree(t, project)

	empty := ""
	component := manifest.Component{Path: ".", Namespace: &empty}
	_, err := layoutComponent(tree, "my-app", component, project)
	require.NoError(t, err)

	// No namespace segment: artifacts land directly under lib/ and share/.
	assert.ElementsMatch(t, []string{
		"lib/tarantool/prebuilt.so",
		"share/tarantool/init.lua",
		"share/tarantool/lib/foo.lua",
	}, relSet(t, tree))
}

func TestLayoutComponent_subdirComponentPath(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	tree := filepath.Join(project, ".rocks")
	writeFile(t, filepath.Join(project, "native", "fast_hash.so"), "so")
	writeFile(t, filepath.Join(project, "native", "fast_hash.c"), "int main(){}")

	compPath := filepath.Join(project, "native")
	_, err := layoutComponent(tree, "my-app", manifest.Component{Path: "native/"}, compPath)
	require.NoError(t, err)

	// Paths are relative to the component root, not the project root.
	assert.ElementsMatch(t, []string{
		"lib/tarantool/my-app/fast_hash.so",
	}, relSet(t, tree))
}
