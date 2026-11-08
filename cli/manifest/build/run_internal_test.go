package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	lrtree "github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// baseManifest is a one-product my-app: a pure-Lua component and a "native"
// component whose .so is produced by a shell backend (so the full orchestration
// runs without a compiler, Tarantool or a registry). It covers every cycle step
// except the live cc compile, which lives in the build-tagged e2e test.
const baseManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.lua]
path = '.'
include = ['*.lua']

[components.native]
path = 'native/'

[components.native.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'echo built > fast_hash.so']
output = ['fast_hash.so']

[products.default]
components = ['lua', 'native']
default = true
`

// setupProject writes a manifest, a VERSION file (so version.Derive is
// deterministic without git) and the given extra files into a fresh temp dir.
func setupProject(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, manifestFileName), manifest)
	writeFile(t, filepath.Join(dir, "VERSION"), "1.2.3\n")

	for name, content := range files {
		writeFile(t, filepath.Join(dir, name), content)
	}

	return dir
}

// dryOptions builds Options that need neither a compiler nor a network: the
// dummy Tarantool facts are enough for shell/make backends and a dependency-free
// closure.
func dryOptions(dir string) Options {
	return Options{
		ProjectDir: dir,
		TtVersion:  "tt test",
		Tarantool:  rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()

	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		require.NoError(t, err)
	}

	return err == nil
}

func TestRun_fullBuild(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":     "-- init",
		"lib/foo.lua":  "-- foo",
		"native/x.txt": "keep native dir",
	})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	tree := filepath.Join(dir, ".rocks")
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/init.lua")))
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/lib/foo.lua")))
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/version.lua")))
	// Default namespace places the native artifact under my-app.
	assert.True(t, exists(t, filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so")))
	// The lock was written.
	assert.True(t, exists(t, filepath.Join(dir, lockFileName)))

	vluaPath := filepath.Join(tree, "share/tarantool/my-app/version.lua")
	data, err := os.ReadFile(vluaPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `version  = "1.2.3"`)

	// The files are the package's rock, which the tree manifest lists.
	view, err := lrtree.Inspect(t.Context(), tree)
	require.NoError(t, err)

	ref, ok := view.Installed("my-app", "1.2.3-1")
	require.True(t, ok)

	owner, _, ok := view.Owner("lib/tarantool/my-app/fast_hash.so")
	require.True(t, ok)
	assert.Equal(t, ref, owner)
}

// TestRun_packageRockFollowsTheVersion pins that a build at another version
// replaces the package's rock, and that a build after an older tt, which laid
// the files out without a rock, takes them over.
func TestRun_packageRockFollowsTheVersion(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":                                  "-- init",
		"native/x.txt":                              "x",
		".rocks/share/tarantool/my-app/init.lua":    "-- laid out by an older tt",
		".rocks/share/tarantool/my-app/version.lua": "-- laid out by an older tt",
	})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	writeFile(t, filepath.Join(dir, "VERSION"), "1.3.0-rc.1\n")
	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	tree := filepath.Join(dir, ".rocks")

	view, err := lrtree.Inspect(t.Context(), tree)
	require.NoError(t, err)
	assert.Equal(t, []string{"1.3.0.rc.1-1"}, view.Versions("my-app"))
	assert.False(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/init.lua~")))

	report, err := openTreeForTest(t, tree).Check(t.Context(), lrtree.CheckOpts{})
	require.NoError(t, err)
	assert.True(t, report.OK(), "%v", report.Issues)
}

// TestRun_packageFileADependencyHolds pins that the package cannot lay a file
// out where a rock of the tree has its own: require would load one of the two.
func TestRun_packageFileADependencyHolds(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua": "-- init", "native/x.txt": "x",
	})

	treetest.Install(t, filepath.Join(dir, ".rocks"), treetest.Rock{
		Name: "squatter", Version: "1.0.0",
		Files: map[string]string{"lua/my-app/init.lua": "return 0\n"},
	})

	err := Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, lrtree.ErrNotPlain)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

// TestRun_productLayingNothingOut pins that a product with nothing to lay
// out is still the package's rock, of its rockspec alone.
func TestRun_productLayingNothingOut(t *testing.T) {
	t.Parallel()

	const manifest = `manifest_version = '0.1'

[package]
name = 'my-app'
generate_version_lua = false

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.lua]
path = 'none'
include = ['*.lua']

[products.default]
components = ['lua']
default = true
`

	dir := setupProject(t, manifest, map[string]string{"none/README": "nothing"})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	view, err := lrtree.Inspect(t.Context(), filepath.Join(dir, ".rocks"))
	require.NoError(t, err)
	assert.Equal(t, []string{"1.2.3-1"}, view.Versions("my-app"))
}

// openTreeForTest opens the tree at root.
func openTreeForTest(t *testing.T, root string) *lrtree.Tree {
	t.Helper()

	rocksTree, err := lrtree.Open(luarocks.Config{Tree: root})
	require.NoError(t, err)

	return rocksTree
}

func TestRun_productSelectsComponentSet(t *testing.T) {
	t.Parallel()

	const manifest = baseManifest + `
[products.minimal]
components = ['lua']
`

	dir := setupProject(t, manifest, map[string]string{"init.lua": "-- init"})

	opts := dryOptions(dir)

	opts.Product = "minimal"
	require.NoError(t, Run(context.Background(), opts))

	tree := filepath.Join(dir, ".rocks")
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/init.lua")))
	// The native component is not part of the minimal product.
	assert.False(t, exists(t, filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so")))
}

func TestRun_emptyNamespaceIsFlat(t *testing.T) {
	t.Parallel()

	const manifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.native]
path = 'native/'
namespace = ''

[components.native.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'echo built > fast_hash.so']
output = ['fast_hash.so']

[products.default]
components = ['native']
default = true
`

	dir := setupProject(t, manifest, map[string]string{"native/x.txt": "x"})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	tree := filepath.Join(dir, ".rocks")
	// Empty namespace: the artifact lands flat under lib/tarantool.
	assert.True(t, exists(t, filepath.Join(tree, "lib/tarantool/fast_hash.so")))
	assert.False(t, exists(t, filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so")))
}

func TestRun_lockedGate(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":           "-- init",
		"native/fast_hash.c": "int x;",
	})

	// First build writes the lock.
	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	// --locked on a fresh lock builds fine.
	locked := dryOptions(dir)

	locked.Locked = true
	require.NoError(t, Run(context.Background(), locked))

	// Change the manifest so the lock goes stale, then --locked must fail (1).
	writeFile(t, filepath.Join(dir, manifestFileName), baseManifest+"\n# drift\n")

	err := Run(context.Background(), locked)
	require.Error(t, err)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

func TestRun_versionLuaCollision(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":           "-- init",
		"version.lua":        "-- shipped by the component",
		"native/fast_hash.c": "int x;",
	})

	// The lua component (include *.lua) lays version.lua into the package
	// namespace, colliding with the generated one.
	err := Run(context.Background(), dryOptions(dir))
	require.Error(t, err)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

func TestRun_generateVersionLuaDisabled(t *testing.T) {
	t.Parallel()

	const manifest = `manifest_version = '0.1'

[package]
name = 'my-app'
generate_version_lua = false

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.lua]
path = '.'
include = ['*.lua']

[products.default]
components = ['lua']
default = true
`

	dir := setupProject(t, manifest, map[string]string{"init.lua": "-- init"})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	tree := filepath.Join(dir, ".rocks")
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/init.lua")))
	assert.False(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/version.lua")))
}

func TestRun_preBuildGeneratedFileIncluded(t *testing.T) {
	t.Parallel()

	const manifest = baseManifest + `
[hooks.pre_build]
backend = 'shell'
command = 'sh'
args = ['-c', 'echo generated > generated.lua']
`

	dir := setupProject(t, manifest, map[string]string{
		"init.lua":           "-- init",
		"native/fast_hash.c": "int x;",
	})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	// pre_build ran before component gathering, so its output is laid out.
	tree := filepath.Join(dir, ".rocks")
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/generated.lua")))
}

func TestRun_fetchMaterializesWithoutBackends(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":           "-- init",
		"native/fast_hash.c": "int x;",
	})

	// Build first so a lock exists.
	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	tree := filepath.Join(dir, ".rocks")
	soPath := filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so")
	vlua := filepath.Join(tree, "share/tarantool/my-app/version.lua")

	require.NoError(t, os.Remove(soPath))
	require.NoError(t, os.Remove(vlua))

	// Fetch runs materialization only; with no deps it neither rebuilds the
	// native artifact nor regenerates version.lua.
	opts := dryOptions(dir)

	opts.FetchOnly = true
	require.NoError(t, Run(context.Background(), opts))

	assert.False(t, exists(t, soPath))
	assert.False(t, exists(t, vlua))
}

func TestRun_fetchWithoutLockFails(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{"init.lua": "-- init"})

	opts := dryOptions(dir)

	opts.FetchOnly = true

	err := Run(context.Background(), opts)
	require.Error(t, err)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

// TestRun_cargoModule builds a module component with cargo: its library is a
// native module of the namespace of the package, and nothing of the target/
// cargo leaves in the component is laid out.
func TestRun_cargoModule(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("cargo is not in PATH")
	}

	const cargoManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.fast]
path = 'fast'

[components.fast.build]
backend = 'cargo'
profile = 'dev'
`

	dir := setupProject(t, cargoManifest, map[string]string{
		"fast/Cargo.toml": "[package]\nname = \"fast\"\nversion = \"0.1.0\"\n" +
			"edition = \"2021\"\n\n[dependencies]\n",
		"fast/src/lib.rs": "#[no_mangle]\npub extern \"C\" fn fast_answer() -> i32 { 42 }\n",
		"fast/init.lua":   "return require('my-app.fast')",
	})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}

	tree := filepath.Join(dir, ".rocks")
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "fast"+ext))
	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "init.lua"))
	assert.DirExists(t, filepath.Join(dir, "fast", "target"))
	assert.NoDirExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "target"))
	assert.NoDirExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "target"))
}
