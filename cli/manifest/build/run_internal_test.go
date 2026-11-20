package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	lrtree "github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
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
		Backends:   buildregtest.Registry(),
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
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/_version.lua")))
	// Default namespace places the native artifact under my-app.
	assert.True(t, exists(t, filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so")))
	// The lock was written.
	assert.True(t, exists(t, filepath.Join(dir, lockFileName)))

	vluaPath := filepath.Join(tree, "share/tarantool/my-app/_version.lua")
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

// TestRun_checkRunsFirst pins that Check is given the manifest and the
// product the build selected, the default one for none, and that its error
// fails the build before anything of it runs.
func TestRun_checkRunsFirst(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":     "-- init",
		"native/x.txt": "keep native dir",
	})

	errRefused := errors.New("refused")

	var checked string

	opts := dryOptions(dir)

	opts.Check = func(man *manifest.Manifest, product string) error {
		assert.Equal(t, "my-app", man.Package.Name)

		checked = product

		return errRefused
	}

	err := Run(context.Background(), opts)
	require.ErrorIs(t, err, errRefused)
	assert.Equal(t, "default", checked)
	assert.False(t, exists(t, filepath.Join(dir, lockFileName)), "nothing was resolved")
	assert.False(t, exists(t, filepath.Join(dir, ".rocks")), "nothing was materialized")
	assert.False(t, exists(t, filepath.Join(dir, "native", "fast_hash.so")), "no backend ran")
}

// TestRun_packageRockFollowsTheVersion pins that a build at another version
// replaces the package's rock, and that a build after an older tt, which laid
// the files out without a rock, takes them over.
func TestRun_packageRockFollowsTheVersion(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":                               "-- init",
		"native/x.txt":                           "x",
		".rocks/share/tarantool/my-app/init.lua": "-- laid out by an older tt",
		".rocks/share/tarantool/my-app/_version.lua": "-- laid out by an older tt",
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

	// A comment and another layout say the same: the lock stays current, and
	// --locked builds.
	writeFile(t, filepath.Join(dir, manifestFileName), "# My app.\n"+
		strings.Replace(baseManifest, "name = 'my-app'", "name = \"my-app\" # the rock", 1)+
		"\n# drift\n")
	require.NoError(t, Run(context.Background(), locked))

	// Change the manifest so the lock goes stale, then --locked must fail (1).
	writeFile(t, filepath.Join(dir, manifestFileName),
		strings.Replace(baseManifest, "tt = '>=3.1.0'", "tt = '>=3.2.0'", 1))

	err := Run(context.Background(), locked)
	require.ErrorIs(t, err, errLockStale)
	assert.Contains(t, err.Error(), "manifest changed since the lock was written")
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

func TestRun_versionLuaCollision(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":           "-- init",
		"_version.lua":       "-- shipped by the component",
		"native/fast_hash.c": "int x;",
	})

	// The lua component (include *.lua) lays _version.lua into the package
	// namespace, colliding with the generated one.
	err := Run(context.Background(), dryOptions(dir))
	require.ErrorIs(t, err, errGeneratedCollision)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	assert.Contains(t, err.Error(), "component ships _version.lua")
}

// TestRun_ownVersionAndRocksLua pins that the plain names are the package's:
// a component shipping a version.lua and a rocks.lua of its own builds, the
// two are laid out as the component's, and the generated _version.lua and
// _rocks.lua sit beside them.
func TestRun_ownVersionAndRocksLua(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":           "-- init",
		"version.lua":        "-- the version module of the component",
		"rocks.lua":          "-- the rocks module of the component",
		"native/fast_hash.c": "int x;",
	})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	share := filepath.Join(dir, ".rocks", "share/tarantool/my-app")

	for name, want := range map[string]string{
		"version.lua": "-- the version module of the component",
		"rocks.lua":   "-- the rocks module of the component",
	} {
		data, err := os.ReadFile(filepath.Join(share, name))
		require.NoError(t, err)
		assert.Equal(t, want, string(data), name)
	}

	vlua, err := os.ReadFile(filepath.Join(share, "_version.lua"))
	require.NoError(t, err)
	assert.Contains(t, string(vlua), `version  = "1.2.3"`)
	assert.Contains(t, readRocksLua(t, dir), `product = "default"`)

	// The four are files of the package's rock.
	view, err := lrtree.Inspect(t.Context(), filepath.Join(dir, ".rocks"))
	require.NoError(t, err)

	ref, ok := view.Installed("my-app", "1.2.3-1")
	require.True(t, ok)

	for _, name := range []string{"version.lua", "rocks.lua", "_version.lua", "_rocks.lua"} {
		owner, _, ok := view.Owner("share/tarantool/my-app/" + name)
		require.True(t, ok, name)
		assert.Equal(t, ref, owner, name)
	}
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
	assert.False(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/_version.lua")))
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

// TestRun_hooksWithoutBackendRunWithShell pins that a hook naming no backend
// runs with shell, the backend validation takes it for.
func TestRun_hooksWithoutBackendRunWithShell(t *testing.T) {
	t.Parallel()

	const manifest = baseManifest + `
[hooks.pre_build]
command = 'sh'
args = ['-c', 'echo generated > generated.lua']

[hooks.post_build]
command = 'sh'
args = ['-c', 'touch post-build-ran']
`

	dir := setupProject(t, manifest, map[string]string{
		"init.lua":           "-- init",
		"native/fast_hash.c": "int x;",
	})

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	tree := filepath.Join(dir, ".rocks")
	assert.True(t, exists(t, filepath.Join(tree, "share/tarantool/my-app/generated.lua")))
	assert.True(t, exists(t, filepath.Join(dir, "post-build-ran")))
}

// TestRun_failedHookNamesItOnce pins the message of a hook that fails: the
// name of the hook once, not "pre_build hook: pre_build hook: ...".
func TestRun_failedHookNamesItOnce(t *testing.T) {
	t.Parallel()

	for _, hook := range []string{"pre_build", "post_build"} {
		manifest := baseManifest + `
[hooks.` + hook + `]
command = 'sh'
args = ['-c', 'exit 7']
`

		dir := setupProject(t, manifest, map[string]string{
			"init.lua":           "-- init",
			"native/fast_hash.c": "int x;",
		})

		err := Run(context.Background(), dryOptions(dir))
		require.Error(t, err)
		assert.Equal(t, 1, strings.Count(err.Error(), hook+" hook"), err.Error())
		assert.True(t, strings.HasPrefix(err.Error(), hook+" hook: "), err.Error())
	}
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
	vlua := filepath.Join(tree, "share/tarantool/my-app/_version.lua")

	require.NoError(t, os.Remove(soPath))
	require.NoError(t, os.Remove(vlua))

	// Fetch runs materialization only; with no deps it neither rebuilds the
	// native artifact nor regenerates _version.lua.
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

// TestRun_excludeOfTheBackend builds a module component of a backend that
// leaves its tool's target/ out of the layout (buildregtest both): its module
// goes into the namespace of the package, the Lua of the component is laid
// out, and a target/ a build of the developer leaves in the component is
// not.
func TestRun_excludeOfTheBackend(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'both'
`, map[string]string{
		"fast/src/lib.rs": "pub fn answer() -> i32 { 42 }\n",
		"fast/init.lua":   "return require('my-app.fast')",
		// What the tool run in the component by hand leaves.
		"fast/target/debug/stray.lua": "return 'stray'",
	})

	tree := filepath.Join(dir, ".rocks")

	for range 2 {
		require.NoError(t, Run(context.Background(), dryOptions(dir)))
		assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "fast.so"))
	}

	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "init.lua"))
	assert.NoDirExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "target"))
	assert.NoDirExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "target"))
}

// TestRun_moduleAndProgramOfOneComponent builds one component whose block
// names a module and a program: the module goes into the namespace of the
// package, the program into .rocks/bin/, both recorded, and the Lua of the
// component is laid out.
func TestRun_moduleAndProgramOfOneComponent(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'both'
module = 'fast'
bin = 'fastctl'
`, map[string]string{"fast/init.lua": "return require('my-app.fast')"})

	tree := filepath.Join(dir, ".rocks")

	result, err := RunResult(context.Background(), dryOptions(dir))
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "fast.so"))
	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "init.lua"))
	assert.NoFileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "fastctl"))
	assert.FileExists(t, filepath.Join(tree, "bin", "fastctl"))

	assert.Equal(t, map[string][]string{"fast": {"fastctl"}}, result.Programs)
	assert.Equal(t, madeFiles{
		Modules: []string{"fast.so"}, Data: nil, Programs: []string{"fastctl"},
	}, recordOf(t, dir)["fast"])
}

// TestRun_workDirOfAPathDependency builds a path dependency: the work
// directory of the build of its component (TT_BUILD_DIR) is in the
// consumer's _build/path/<name>/, and nothing is written into the directory
// of the dependency, another checkout.
func TestRun_workDirOfAPathDependency(t *testing.T) {
	t.Parallel()

	app, _ := setupNeighbours(t)
	shdep := filepath.Join(filepath.Dir(app), "shdep")

	writeFile(t, filepath.Join(shdep, manifestFileName), `manifest_version = '0.1'

[package]
name = 'shdep'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.fast]
path = 'fast'

[components.fast.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'echo work > "$TT_BUILD_DIR/x.o" && cp "$TT_BUILD_DIR/x.o" "$TT_OUTPUT_DIR/fast.so"']
`)
	writeFile(t, filepath.Join(shdep, "VERSION"), "0.1.0\n")
	writeFile(t, filepath.Join(shdep, "fast", "fast.c"), "int answer;\n")
	writeFile(t, filepath.Join(app, manifestFileName), strings.Replace(consumerManifest,
		"[dependencies]\n", "[dependencies]\nshdep = { source = 'path', path = '../shdep' }\n", 1))

	require.NoError(t, Run(context.Background(), dryOptions(app)))

	assert.FileExists(t, filepath.Join(app, rocksDirName, "lib", "tarantool", "shdep", "fast.so"))
	assert.FileExists(t, filepath.Join(pathState(app, "shdep"), componentDirName, "fast", "x.o"))
	assert.NoDirExists(t, filepath.Join(shdep, buildDirName))
}

// TestRun_buildDirOfAShellBuild pins that a build gets TT_BUILD_DIR, the
// directory of its intermediate files in _build/, there when it starts and
// kept between builds, and emptied by --rebuild.
func TestRun_buildDirOfAShellBuild(t *testing.T) {
	t.Parallel()

	const manifest = manifestHead + `
[components.native]
path = 'native'

[components.native.build]
backend = 'shell'
command = 'sh'
args = ['-c', '''
if [ -f "$TT_BUILD_DIR/x.o" ]; then echo kept; else echo made; fi >> ../builds.log
echo "$TT_BUILD_DIR" > "$TT_BUILD_DIR/x.o"
mkdir -p "$TT_OUTPUT_DIR" && cp "$TT_BUILD_DIR/x.o" "$TT_OUTPUT_DIR/x.so"
''']
`

	dir := setupProject(t, manifest, map[string]string{"init.lua": "-- init", "native/x.c": ""})
	builds := func() string {
		t.Helper()

		data, err := os.ReadFile(filepath.Join(dir, "builds.log"))
		require.NoError(t, err)

		return strings.Join(strings.Fields(string(data)), " ")
	}

	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	told, err := os.ReadFile(filepath.Join(dir, rocksDirName, "lib/tarantool/my-app/x.so"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, buildDirName, componentDirName, "native")+"\n",
		string(told))
	assert.Equal(t, "made", builds())

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, "made kept", builds())

	rebuild := dryOptions(dir)

	rebuild.Rebuild = true

	require.NoError(t, Run(context.Background(), rebuild))
	assert.Equal(t, "made kept made", builds())
}
