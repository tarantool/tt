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

// writePathDep creates a path-dependency directory with one file and returns it,
// so contentHash has real content to hash.
func writePathDep(t *testing.T, projectDir, rel string) string {
	t.Helper()

	dir := filepath.Join(projectDir, rel)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.lua"), []byte("return {}\n"), 0o600))

	return dir
}

// TestRegistryOverrideSurvivesEarlierTransitive guards that a direct dependency's
// registry override is honored even when an alphabetically-earlier sibling pulls
// the same rock transitively from the default servers first.
func TestRegistryOverrideSurvivesEarlierTransitive(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("applib", "1.0.0-1", "ap", dep(t, "metrics", ">=1.0")).
		add("metrics", "1.0.0-1", "default-md5").
		addScoped("https://custom", "metrics", "2.0.0-1", "custom-md5")

	man := parseManifest(t, oneProduct+`[dependencies]
applib = '>=1.0.0'
[dependencies.metrics]
source = 'registry'
version = '>=1.0.0'
registry = 'https://custom'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// applib sorts before metrics and requires it transitively, but the direct
	// override must still route metrics to the custom server (2.0.0-1), not the
	// default server's 1.0.0-1.
	got := findDep(t, lock.Products["default"].Dependencies, "metrics")
	assert.Equal(t, "2.0.0-1", got.Version)
	assert.Equal(t, "md5:custom-md5", got.Checksum)
}

// TestPathDepSurvivesEarlierTransitive guards that a direct path dependency is
// pinned from its local source even when an alphabetically-earlier sibling pulls
// the same name transitively from a registry first.
func TestPathDepSurvivesEarlierTransitive(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	vendor := writePathDep(t, projectDir, filepath.Join("vendor", "foo"))

	fake := newFakeAdapter().
		add("bar", "1.0.0-1", "b", dep(t, "foo", ">=1.0")).
		add("foo", "9.9.9-1", "registry-foo"). // Would wrongly win without the fix.
		addLocal(vendor, "foo", "1.0.0-1")

	man := parseManifest(t, oneProduct+`[dependencies]
bar = '>=1.0.0'
[dependencies.foo]
source = 'path'
path = 'vendor/foo'
`)

	engine := resolve.NewEngine(fake, projectDir, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// bar sorts before foo and requires it transitively, but foo is a path
	// dependency and must be pinned from the local source, not the registry.
	foo := findDep(t, lock.Products["default"].Dependencies, "foo")
	assert.Equal(t, "path", foo.Source)
	assert.Equal(t, "vendor/foo", foo.Path)
	assert.Equal(t, "1.0.0-1", foo.Version)
	assert.NotEmpty(t, foo.ContentHash)
}

// TestLeafPathDepSatisfiesTransitiveConstraint guards that a leaf path dependency
// (one shipping no rockspec, so its version is unknown) is not falsely rejected
// when another branch constrains the same name - a path dependency is an explicit
// local override and satisfies any version constraint by fiat.
func TestLeafPathDepSatisfiesTransitiveConstraint(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writePathDep(t, projectDir, filepath.Join("vendor", "foo"))

	// foo ships no local rockspec (leaf, zero version); zbar transitively
	// requires foo >=1.0. Before the fix this raised a spurious ErrConflict.
	fake := newFakeAdapter().
		add("zbar", "1.0.0-1", "z", dep(t, "foo", ">=1.0"))

	man := parseManifest(t, oneProduct+`[dependencies]
zbar = '>=1.0.0'
[dependencies.foo]
source = 'path'
path = 'vendor/foo'
`)

	engine := resolve.NewEngine(fake, projectDir, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	foo := findDep(t, lock.Products["default"].Dependencies, "foo")
	assert.Equal(t, "path", foo.Source)
	assert.NotEmpty(t, foo.ContentHash)
}

// TestDependencyCycleDetected guards the cycle guard: a rock depending on a rock
// that transitively depends back on it aborts with ErrCycle and the offending
// chain, instead of recursing forever.
func TestDependencyCycleDetected(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("aa", "1.0.0-1", "a", dep(t, "bb", ">=1.0")).
		add("bb", "1.0.0-1", "b", dep(t, "aa", ">=1.0"))

	man := parseManifest(t, oneProduct+`[dependencies]
aa = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	_, _, err := engine.Resolve(context.Background(), man)
	require.ErrorIs(t, err, resolve.ErrCycle)
	assert.Contains(t, err.Error(), "aa")
	// The chain that closed the loop is reported.
	assert.Contains(t, err.Error(), "->")
}

// TestSharedMissingMD5WarnsOnce guards that a checksum-less rock shared by several
// products yields a single reproducibility warning, not one per product.
func TestSharedMissingMD5WarnsOnce(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().add("metrics", "1.0.0-1", "") // No md5.

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

	_, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "no md5")
}
