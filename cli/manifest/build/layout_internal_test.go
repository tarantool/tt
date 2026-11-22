package build

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
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
	stage := filepath.Join(project, "_build", "rock", "default")
	seedComponentTree(t, project)

	written, err := layoutComponent(stage, "my-app", manifest.Component{Path: "."}, project, nil)
	require.NoError(t, err)

	// .lua goes to lua, .so to lib, both under the my-app namespace; the
	// README, C source, test/, hidden and manifest files are all filtered out.
	assert.ElementsMatch(t, []string{
		"lib/my-app/prebuilt.so",
		"lua/my-app/init.lua",
		"lua/my-app/lib/foo.lua",
	}, relSet(t, stage))

	// Returned destinations are the absolute paths written.
	assert.Contains(t, written, filepath.Join(stage, "lua/my-app/init.lua"))
	assert.Len(t, written, 3)
}

func TestLayoutComponent_dylibGoesToLib(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	stage := filepath.Join(project, "_build", "rock", "default")
	writeFile(t, filepath.Join(project, "native", "fast_hash.dylib"), "dylib")

	component := manifest.Component{Path: "native/", Include: []string{"*.dylib"}}
	_, err := layoutComponent(stage, "my-app", component, filepath.Join(project, "native"), nil)
	require.NoError(t, err)

	// A module of macOS is a module all the same: Tarantool loads it from lib.
	assert.ElementsMatch(t, []string{
		"lib/my-app/fast_hash.dylib",
	}, relSet(t, stage))
}

func TestLayoutComponent_emptyNamespaceIsFlat(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	stage := filepath.Join(project, "_build", "rock", "default")
	seedComponentTree(t, project)

	empty := ""
	component := manifest.Component{Path: ".", Namespace: &empty}
	_, err := layoutComponent(stage, "my-app", component, project, nil)
	require.NoError(t, err)

	// No namespace segment: artifacts land directly under lib/ and lua/.
	assert.ElementsMatch(t, []string{
		"lib/prebuilt.so",
		"lua/init.lua",
		"lua/lib/foo.lua",
	}, relSet(t, stage))
}

func TestLayoutComponent_subdirComponentPath(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	stage := filepath.Join(project, "_build", "rock", "default")
	writeFile(t, filepath.Join(project, "native", "fast_hash.so"), "so")
	writeFile(t, filepath.Join(project, "native", "fast_hash.c"), "int main(){}")

	compPath := filepath.Join(project, "native")
	_, err := layoutComponent(stage, "my-app", manifest.Component{Path: "native/"}, compPath, nil)
	require.NoError(t, err)

	// Paths are relative to the component root, not the project root.
	assert.ElementsMatch(t, []string{
		"lib/my-app/fast_hash.so",
	}, relSet(t, stage))
}

// TestLayoutOf: the layout of a file that is not there is what the layout
// of each component would take of it were it there - by its include and
// exclude, the default ones among them, under its namespace - as the layout
// of the files that are takes them.
func TestLayoutOf(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[components.native]
path = 'native'
namespace = 'fast'
exclude = ['skip/']

[components.data]
path = 'data'
include = ['*.json']
`, map[string]string{"init.lua": "return 1"})

	man, err := readManifest(dir, dryOptions(dir))
	require.NoError(t, err)

	got := LayoutOf(dir, man, "default", dryOptions(dir).backends(), []string{
		"native/out/hash.so", "native/hash.lua", "native/skip/x.so", "native/test/y.so",
		"native/hash.c", "data/a.json", "data/a.lua", "other/z.so", "init.lua",
	})

	// The components are in the order of the product. The one at the root
	// takes the Lua of the others too, and its own init.lua, which is there,
	// as Result.Layout has it.
	assert.Equal(t, []LaidOut{
		{Component: "data", Source: "data/a.json", Dest: "lua/my-app/a.json"},
		{Component: "lua", Source: "native/hash.lua", Dest: "lua/my-app/native/hash.lua"},
		{Component: "lua", Source: "data/a.lua", Dest: "lua/my-app/data/a.lua"},
		{Component: "lua", Source: "init.lua", Dest: "lua/my-app/init.lua"},
		{Component: "native", Source: "native/out/hash.so", Dest: "lib/fast/out/hash.so"},
		{Component: "native", Source: "native/hash.lua", Dest: "lua/fast/hash.lua"},
	}, got)
}

// TestResultLayout: a build reports what the layout of each component took,
// from where in the project to where in the stage, and the _version.lua it
// generated, as the stage has them; Prepare, which builds nothing, reports
// the same of a build at the same time.
func TestResultLayout(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[components.native]
path = 'native'
namespace = 'fast'
`, map[string]string{
		"init.lua":         "return 1",
		"native/hash.so":   "so",
		"native/hash.lua":  "return 2",
		"native/README.md": "not laid out",
	})

	opts := dryOptions(dir)

	opts.Now = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	built, err := RunResult(context.Background(), opts)
	require.NoError(t, err)

	// The include of the component at the root, *.lua, takes the Lua of the
	// other one too.
	assert.Equal(t, []LaidOut{
		{Component: "lua", Source: "init.lua", Dest: "lua/my-app/init.lua"},
		{Component: "lua", Source: "native/hash.lua", Dest: "lua/my-app/native/hash.lua"},
		{Component: "native", Source: "native/hash.lua", Dest: "lua/fast/hash.lua"},
		{Component: "native", Source: "native/hash.so", Dest: "lib/fast/hash.so"},
	}, built.Layout)

	for _, file := range built.Layout {
		assert.FileExists(t, filepath.Join(built.Stage, filepath.FromSlash(file.Dest)))
	}

	assert.Equal(t, "lua/my-app/_version.lua", built.VersionLua.Dest)

	staged, err := os.ReadFile(filepath.Join(built.Stage, "lua", "my-app", "_version.lua"))
	require.NoError(t, err)
	assert.Equal(t, string(staged), built.VersionLua.Content)
	assert.Contains(t, built.VersionLua.Content, `built_at = "2026-11-22T10:00:00Z",`)

	prepared, err := Prepare(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, built.Layout, prepared.Layout)
	assert.Equal(t, built.VersionLua, prepared.VersionLua)
}
