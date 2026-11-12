package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	lrtree "github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// contribManifest is a neighbour project built by its consumer: a Lua
// component and a native one whose shell build writes into TT_OUTPUT_DIR what
// it was run with, and a pre_build hook its consumer must not run. The hook
// writes into _build/, so the neighbour's own build of itself changes nothing
// the consumer's lock covers.
const contribManifest = `manifest_version = '0.1'

[package]
name = 'contrib'
description = 'A neighbour'

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
args = ['-c', '''
mkdir -p "$TT_OUTPUT_DIR"
pwd -P > "$TT_OUTPUT_DIR/where.so"
echo "$TT_PROJECT_ROOT" >> "$TT_OUTPUT_DIR/where.so"
''']

[products.default]
components = ['lua', 'native']
default = true

[hooks.pre_build]
backend = 'shell'
command = 'sh'
args = ['-c', 'mkdir -p _build && touch _build/hook-ran']
`

// consumerManifest depends on contrib by path.
const consumerManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[dependencies]
contrib = { source = 'path', path = '../contrib' }

[components.lua]
path = '.'
include = ['*.lua']
`

// setupNeighbours lays out the consumer at <base>/app and contrib at
// <base>/contrib, with a rockspec beside contrib's manifest, and returns both
// directories.
func setupNeighbours(t *testing.T) (string, string) {
	t.Helper()

	base := t.TempDir()
	app := filepath.Join(base, "app")
	contrib := filepath.Join(base, "contrib")

	writeFile(t, filepath.Join(app, manifestFileName), consumerManifest)
	writeFile(t, filepath.Join(app, "VERSION"), "1.2.3\n")
	writeFile(t, filepath.Join(app, "init.lua"), "return require('contrib')\n")

	writeFile(t, filepath.Join(contrib, manifestFileName), contribManifest)
	writeFile(t, filepath.Join(contrib, "VERSION"), "0.3.0-rc.1\n")
	writeFile(t, filepath.Join(contrib, ".gitignore"), "/_build/\n/.rocks/\n")
	writeFile(t, filepath.Join(contrib, "init.lua"), "return { contrib = true }\n")
	writeFile(t, filepath.Join(contrib, "util.lua"), "return 1\n")
	writeFile(t, filepath.Join(contrib, "native", "where.c"), "int x;\n")
	writeFile(t, filepath.Join(contrib, "contrib-scm-1.rockspec"), "package = 'contrib'\n")

	return app, contrib
}

// TestRun_pathManifestDependency pins that a path dependency holding a manifest
// is built by tt's own pipeline into the consumer's _build/ and installed into
// the consumer's tree as the rock of its package, nothing being written into
// its own directory and its hooks not run.
func TestRun_pathManifestDependency(t *testing.T) {
	t.Parallel()

	app, contrib := setupNeighbours(t)

	var warnings []string

	opts := dryOptions(app)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	require.NoError(t, Run(context.Background(), opts))

	tree := filepath.Join(app, rocksDirName)
	share := filepath.Join(tree, "share/tarantool/contrib")
	assert.True(t, exists(t, filepath.Join(share, "init.lua")))
	assert.True(t, exists(t, filepath.Join(share, "util.lua")))

	vlua, err := os.ReadFile(filepath.Join(share, "version.lua"))
	require.NoError(t, err)
	assert.Contains(t, string(vlua), `version  = "0.3.0-rc.1"`)

	// The native build ran in the neighbour, with the neighbour as its root,
	// and wrote into the consumer's stage.
	where, err := os.ReadFile(filepath.Join(tree, "lib/tarantool/contrib/where.so"))
	require.NoError(t, err)

	realContrib, err := filepath.EvalSymlinks(contrib)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(realContrib, "native"), contrib},
		strings.Fields(string(where)))

	stage := filepath.Join(pathState(app, "contrib"), stageDirName, "default")
	assert.True(t, exists(t, filepath.Join(stage, "lua/contrib/init.lua")))

	// The neighbour is the rock of its package, its files owned by it, and the
	// consumer's own rock sits beside it.
	view, err := lrtree.Inspect(t.Context(), tree)
	require.NoError(t, err)
	assert.Equal(t, []string{"0.3.0.rc.1-1"}, view.Versions("contrib"))
	assert.Equal(t, []string{"1.2.3-1"}, view.Versions("my-app"))

	owner, _, ok := view.Owner("share/tarantool/contrib/init.lua")
	require.True(t, ok)
	assert.Equal(t, "contrib", owner.Name)

	// Nothing of the build went into the neighbour's directory.
	for _, name := range []string{buildDirName, rocksDirName, lockFileName} {
		assert.False(t, exists(t, filepath.Join(contrib, name)), name)
	}

	// The resolver, which read the neighbour's manifest, warned once that its
	// hooks are not run.
	joined := strings.Join(warnings, "\n")
	assert.Contains(t, joined, "contrib-scm-1.rockspec")
	assert.Equal(t, 1, strings.Count(joined, `path dependency "contrib" declares the hooks `+
		`pre_build; the hooks of a path dependency are not run`), joined)

	// The lock names the rock that was installed.
	lock, err := loadLock(app)
	require.NoError(t, err)

	pinned := map[string]manifest.LockDependency{}
	for _, dep := range lock.Products[manifest.ImplicitProduct].Dependencies {
		pinned[dep.Name] = dep
	}

	assert.Equal(t, "0.3.0.rc.1-1", pinned["contrib"].Version)
	assert.Equal(t, "../contrib", pinned["contrib"].Path)

	// A second build, from the fresh lock, installs the same rock again, and
	// resolves nothing to warn of.
	warnings = nil

	require.NoError(t, Run(context.Background(), opts))

	view, err = lrtree.Inspect(t.Context(), tree)
	require.NoError(t, err)
	assert.Equal(t, []string{"0.3.0.rc.1-1"}, view.Versions("contrib"))
	assert.Empty(t, warnings)
}

// TestRun_pathDependencyByAbsolutePath pins that a path dependency given by an
// absolute path is built from that directory, not from one under the project,
// and that the lock keeps the path as it is written.
func TestRun_pathDependencyByAbsolutePath(t *testing.T) {
	t.Parallel()

	app, contrib := setupNeighbours(t)
	writeFile(t, filepath.Join(app, manifestFileName), strings.Replace(consumerManifest,
		"path = '../contrib'", "path = '"+filepath.ToSlash(contrib)+"'", 1))

	opts := dryOptions(app)
	require.NoError(t, Run(context.Background(), opts))

	share := filepath.Join(app, rocksDirName, "share/tarantool/contrib")
	assert.True(t, exists(t, filepath.Join(share, "init.lua")))

	lock, err := loadLock(app)
	require.NoError(t, err)

	deps := lock.Products[manifest.ImplicitProduct].Dependencies
	require.Len(t, deps, 1)
	assert.Equal(t, filepath.ToSlash(contrib), deps[0].Path)

	// The lock is in step with the directory the path names.
	opts.Locked = true
	require.NoError(t, Run(context.Background(), opts))
}

// TestRun_leafPathDependency pins that a path dependency with nothing to
// build, neither a manifest nor a rockspec, installs nothing and is no
// dependency of the rock of the package: the lock pins it at no version, and
// the rockspec of the rock was made with leaf == and failed to parse.
func TestRun_leafPathDependency(t *testing.T) {
	t.Parallel()

	app, _ := setupNeighbours(t)
	writeFile(t, filepath.Join(filepath.Dir(app), "leaf", "leaf.lua"), "return 1\n")
	writeFile(t, filepath.Join(app, manifestFileName), strings.Replace(consumerManifest,
		"contrib = { source = 'path', path = '../contrib' }",
		"leaf = { source = 'path', path = '../leaf' }", 1))

	require.NoError(t, Run(context.Background(), dryOptions(app)))

	lock, err := loadLock(app)
	require.NoError(t, err)
	assert.Equal(t, []manifest.LockDependency{{
		Name: "leaf", Source: sourcePath, Path: "../leaf",
		ContentHash: lock.Products[manifest.ImplicitProduct].Dependencies[0].ContentHash,
	}}, lock.Products[manifest.ImplicitProduct].Dependencies)

	view, err := lrtree.Inspect(t.Context(), filepath.Join(app, rocksDirName))
	require.NoError(t, err)
	assert.Equal(t, []string{"1.2.3-1"}, view.Versions("my-app"))
	assert.Empty(t, view.Versions("leaf"))
}

// TestPathManifests_hookWarning pins that the build of a path dependency does
// not warn that its hooks are not run - the resolver did, reading its
// manifest - and that the build of a source rock, which is not resolved from
// its manifest, does.
func TestPathManifests_hookWarning(t *testing.T) {
	t.Parallel()

	app, contrib := setupNeighbours(t)

	var warnings []string

	opts := dryOptions(app)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       filepath.Join(app, rocksDirName),
		WorkingDir: app,
		Servers:    nil,
		Logger:     nil,
	}))
	builder := newPathManifests(opts, adapter)
	dep := manifest.LockDependency{
		Name: "contrib", Version: "0.3.0.rc.1-1", Source: sourcePath, Path: "../contrib",
	}

	require.NoError(t, builder.buildPathManifest(t.Context(), dep))
	assert.Empty(t, warnings)

	require.NoError(t, builder.buildManifest(t.Context(), manifestBuild{
		dep: dep, dir: contrib, state: filepath.Join(app, buildDirName, srcDirName, "contrib"),
		revision: 1, what: "source rock", stale: "", warnHooks: true,
	}))
	assert.Equal(t, []string{`source rock "contrib" declares the hooks pre_build; ` +
		`the hooks of a source rock are not run`}, warnings)
}

// TestRun_pathManifestHashSurvivesItsOwnBuild pins that the neighbour building
// itself - its _build/, .rocks/ and lock - does not make the consumer's lock
// stale, so a --locked build of the consumer still goes through.
func TestRun_pathManifestHashSurvivesItsOwnBuild(t *testing.T) {
	t.Parallel()

	app, contrib := setupNeighbours(t)

	require.NoError(t, Run(context.Background(), dryOptions(app)))

	// The neighbour's own build runs its hook and writes its own state.
	require.NoError(t, Run(context.Background(), dryOptions(contrib)))
	require.True(t, exists(t, filepath.Join(contrib, buildDirName, "hook-ran")))
	require.True(t, exists(t, filepath.Join(contrib, lockFileName)))

	man, err := readManifest(app, Options{})
	require.NoError(t, err)

	lock, err := loadLock(app)
	require.NoError(t, err)

	stale, reason, err := resolve.IsStale(app, man, lock)
	require.NoError(t, err)
	assert.False(t, stale, reason)

	opts := dryOptions(app)

	opts.Locked = true
	require.NoError(t, Run(context.Background(), opts))
}

// TestPathManifests_versionTheLockDoesNotPin pins that a neighbour whose
// version moved away from the lock is refused rather than installed at a
// version the lock does not name.
func TestPathManifests_versionTheLockDoesNotPin(t *testing.T) {
	t.Parallel()

	app, _ := setupNeighbours(t)

	opts := dryOptions(app)
	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       filepath.Join(app, rocksDirName),
		WorkingDir: app,
		Servers:    nil,
		Logger:     nil,
	}))

	err := newPathManifests(opts, adapter).buildPathManifest(t.Context(),
		manifest.LockDependency{
			Name: "contrib", Version: "0.2.0-1", Source: sourcePath, Path: "../contrib",
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the lock pins 0.2.0-1")
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	assert.False(t, exists(t, filepath.Join(app, rocksDirName)))
}

// TestMaterialize_pathManifestWinsOverRockspec pins that a path dependency
// holding a manifest is built from the manifest, the rockspec beside it left
// alone.
func TestMaterialize_pathManifestWinsOverRockspec(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "vendor", "mylib", manifestFileName), "# manifest")
	writeFile(t, filepath.Join(project, "vendor", "mylib", "mylib-scm-1.rockspec"), "-- spec")

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "mylib", Version: "1.0.0-1", Source: sourcePath, Path: "vendor/mylib"},
	}}

	require.NoError(t, materialize(context.Background(), fake, fake, fake, project, prod, nil,
		rockServers{}))
	assert.Equal(t, []string{"mylib"}, fake.manifests)
	assert.Empty(t, fake.builds)
}
