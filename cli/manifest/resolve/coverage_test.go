package resolve_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/resolve"
)

// TestRegistryOverrideRoutesToNamedServer checks that a dependency's registry
// override is threaded to the adapter: the rock is taken from the named
// registry, not the default servers.
func TestRegistryOverrideRoutesToNamedServer(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.0.0-1", "default").
		addScoped("https://custom", "metrics", "2.0.0-1", "custom")

	man := parseManifest(t, oneProduct+`[dependencies.metrics]
source = 'registry'
version = '>=1.0.0'
registry = 'https://custom'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// The default server would have given 1.0.0-1; the override routes to the
	// custom registry's 2.0.0-1.
	got := findDep(t, lock.Products["default"].Dependencies, "metrics")
	assert.Equal(t, "2.0.0-1", got.Version)
	assert.Equal(t, "md5:custom", got.Checksum)
}

// TestPathDependencyTransitiveResolves checks that a path dependency's local
// rockspec contributes its version and its transitive registry dependencies to
// the closure.
func TestPathDependencyTransitiveResolves(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	vendor := filepath.Join(projectDir, "vendor", "mymod")
	require.NoError(t, os.MkdirAll(vendor, 0o750))

	initLua := filepath.Join(vendor, "init.lua")
	require.NoError(t, os.WriteFile(initLua, []byte("return {}\n"), 0o600))

	fake := newFakeAdapter().
		add("checks", "3.1.0-1", "c").
		addLocal(vendor, "mymod", "1.0.0-1", dep(t, "checks", ">=3.0"))

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies.mymod]
source = 'path'
path = 'vendor/mymod'
[components.app]
path = '.'
[products.default]
components = ['app']
default = true
`)

	engine := resolve.NewEngine(fake, projectDir, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	dependencies := lock.Products["default"].Dependencies
	// Topological order: the path dep's transitive checks precedes mymod.
	assert.Equal(t, []string{"checks", "mymod"}, depNames(dependencies))

	mymod := findDep(t, dependencies, "mymod")
	assert.Equal(t, "path", mymod.Source)
	assert.Equal(t, "1.0.0-1", mymod.Version)
	assert.NotEmpty(t, mymod.ContentHash)

	checks := findDep(t, dependencies, "checks")
	assert.Equal(t, "registry", checks.Source)
	assert.Equal(t, "3.1.0-1", checks.Version)
}

// TestSharedDependencyResolvedOncePerRun checks the cross-product cache: two
// products depending on the same rock resolve and fetch it once, not once per
// product.
func TestSharedDependencyResolvedOncePerRun(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().add("metrics", "1.0.0-1", "m")

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[components.c1]
path = 'a'
[components.c1.dependencies]
metrics = '>=1.0.0'
[components.c2]
path = 'b'
[components.c2.dependencies]
metrics = '>=1.0.0'
[products.p1]
components = ['c1']
default = true
[products.p2]
components = ['c2']
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// Both products pin the same version.
	assert.Equal(t, "1.0.0-1", findDep(t, lock.Products["p1"].Dependencies, "metrics").Version)
	assert.Equal(t, "1.0.0-1", findDep(t, lock.Products["p2"].Dependencies, "metrics").Version)

	// But the registry was queried and the rockspec fetched exactly once.
	assert.Equal(t, 1, fake.resolves["metrics"])
	assert.Equal(t, 1, fake.metadatas["metrics"])
}

// TestMetadataCacheIsRegistryDistinct guards against sharing a rockspec across
// products when the same name and version resolve from different servers: the
// metadata cache is keyed by artifact URL, so a per-registry override gets its
// own server's checksum, not another product's.
func TestMetadataCacheIsRegistryDistinct(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("foo", "1.0.0-1", "default-md5").
		addScoped("https://custom", "foo", "1.0.0-1", "custom-md5")

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[components.c1]
path = 'a'
[components.c1.dependencies]
foo = '>=1.0.0'
[components.c2]
path = 'b'
[components.c2.dependencies.foo]
source = 'registry'
version = '>=1.0.0'
registry = 'https://custom'
[products.p1]
components = ['c1']
default = true
[products.p2]
components = ['c2']
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// Same name and version, but each product's checksum comes from its own
	// server's rockspec - the cache must not share one across the two.
	assert.Equal(t, "md5:default-md5", findDep(t, lock.Products["p1"].Dependencies, "foo").Checksum)
	assert.Equal(t, "md5:custom-md5", findDep(t, lock.Products["p2"].Dependencies, "foo").Checksum)
}

// TestDeclarationConflictWithoutRegistry checks that a global-vs-component
// version conflict is detected structurally, before any registry lookup - even
// when the rock is not published anywhere.
func TestDeclarationConflictWithoutRegistry(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter() // empty: metrics is not published on any server

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
metrics = '==1.0.0-1'
[components.app]
path = '.'
[components.app.dependencies]
metrics = '==2.0.0-1'
[products.default]
components = ['app']
default = true
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	_, _, err := engine.Resolve(context.Background(), man)
	require.ErrorIs(t, err, resolve.ErrConflict)
	assert.Contains(t, err.Error(), "metrics")

	// The conflict is structural: the registry is never consulted.
	assert.Equal(t, 0, fake.resolves["metrics"])
}
