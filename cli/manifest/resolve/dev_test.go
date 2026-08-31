package resolve_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
)

// devHelperDir creates a path-dependency directory under root and returns its
// absolute path.
func devHelperDir(t *testing.T, root, name string) string {
	t.Helper()

	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.lua"), []byte("return {}\n"), 0o600))

	return dir
}

// TestResolveDevDependenciesLandInLock is the base case: [dev_dependencies]
// resolves into its own closure, transitives included, and the runtime closure
// is untouched by their presence.
func TestResolveDevDependenciesLandInLock(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "aaa").
		add("luatest", "1.0.1-1", "bbb", dep(t, "checks", ">=3.0")).
		add("checks", "3.1.0-1", "ccc")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
[dev_dependencies]
luatest = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	// The runtime closure stays exactly what it was: one rock, no dev rocks.
	assert.Equal(t, []string{"metrics"}, depNames(lock.Products["default"].Dependencies))

	// The dev closure is transitive, in the same deepest-first order.
	assert.Equal(t, []string{"checks", "luatest"}, depNames(lock.DevDependencies))
	assert.Equal(t, "1.0.1-1", findDep(t, lock.DevDependencies, "luatest").Version)
	assert.Equal(t, "md5:ccc", findDep(t, lock.DevDependencies, "checks").Checksum)
}

// TestResolveNoDevDependenciesLeavesClosureEmpty pins that a manifest without
// the table resolves to no dev closure at all rather than an empty-but-present
// one, which is what keeps existing locks byte-identical.
func TestResolveNoDevDependenciesLeavesClosureEmpty(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(newFakeAdapter().add("metrics", "1.5.0-1", "aaa"), "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Nil(t, lock.DevDependencies)
}

// TestResolveDevSharedRockTakesRuntimePick is the reason the dev closure is
// resolved against the products rather than beside them: both land in one
// .rocks/ tree, which cannot hold two versions of a rock. The dev constraint
// here would take the newest on its own; pinned to the runtime pick it does
// not.
func TestResolveDevSharedRockTakesRuntimePick(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("common", "1.5.0-1", "aaa").
		add("common", "2.0.0-1", "bbb").
		add("luatest", "1.0.1-1", "ccc", dep(t, "common", ">=1.0.0"))

	man := parseManifest(t, oneProduct+`[dependencies]
common = '>=1.0.0,<2.0.0'
[dev_dependencies]
luatest = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	runtimePick := findDep(t, lock.Products["default"].Dependencies, "common").Version
	devPick := findDep(t, lock.DevDependencies, "common").Version

	assert.Equal(t, "1.5.0-1", runtimePick)
	assert.Equal(t, runtimePick, devPick,
		"a rock in both closures must resolve to one version, the runtime pick")
}

// TestResolveDevIncompatibleConstraintDropsPin covers the accepted escape
// hatch: a dev-only constraint the runtime pick cannot satisfy drops the pin
// and warns rather than failing the resolution. The tree then genuinely holds
// two versions, which the warning is there to say.
func TestResolveDevIncompatibleConstraintDropsPin(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("common", "1.5.0-1", "aaa").
		add("common", "2.0.0-1", "bbb")

	man := parseManifest(t, oneProduct+`[dependencies]
common = '<2.0.0'
[dev_dependencies]
common = '>=2.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	assert.Equal(t, "1.5.0-1",
		findDep(t, lock.Products["default"].Dependencies, "common").Version)
	assert.Equal(t, "2.0.0-1", findDep(t, lock.DevDependencies, "common").Version)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "common")
}

// TestResolveDevPathDependency pins that a path-sourced dev dependency goes
// through the ordinary path machinery: it is content-hashed into the dev
// closure like any runtime path dependency, with no special case.
func TestResolveDevPathDependency(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "aaa").
		addLocal(devHelperDir(t, projectDir, "helper"), "helper", "0.1.0")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
[dev_dependencies.helper]
source = 'path'
path = 'helper'
`)

	engine := resolve.NewEngine(fake, projectDir, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	helper := findDep(t, lock.DevDependencies, "helper")
	assert.Equal(t, "path", helper.Source)
	assert.Equal(t, "helper", helper.Path)
	assert.NotEmpty(t, helper.ContentHash)
}

// TestIsStaleDevClosureMissing is the upgrade case: a lock written before tt
// resolved [dev_dependencies] carries none, and its manifest_hash still
// matches because the manifest never changed. Nothing but this check can
// notice, so without it such a project silently never gets its dev
// dependencies again.
func TestIsStaleDevClosureMissing(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct+`[dev_dependencies]
luatest = '>=1.0.0'
`)

	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		ManifestHash:    man.Hash(),
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: nil},
		},
	}

	engine := resolve.NewEngine(newFakeAdapter(), "", "tt 3.4.0")

	stale, reason, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.True(t, stale)
	assert.Contains(t, reason, "dev dependencies")
}

// TestIsStaleDevClosurePresent is the other half: once the closure is in the
// lock, the same manifest is not stale. Without it the check above would pass
// for an implementation that reported every manifest with dev dependencies as
// permanently stale.
func TestIsStaleDevClosurePresent(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct+`[dev_dependencies]
luatest = '>=1.0.0'
`)

	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		ManifestHash:    man.Hash(),
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: nil},
		},
		DevDependencies: []manifest.LockDependency{
			{Name: "luatest", Version: "1.0.1-1", Source: "registry"},
		},
	}

	engine := resolve.NewEngine(newFakeAdapter(), "", "tt 3.4.0")

	stale, reason, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.False(t, stale, "reason: %s", reason)
}

// TestIsStaleDevPathDependencyChanged pins that a dev path dependency is
// content-hashed like a runtime one: editing the directory makes the lock
// stale, rather than the dev closure being a hole in the check.
func TestIsStaleDevPathDependencyChanged(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	devHelperDir(t, projectDir, "helper")

	man := parseManifest(t, oneProduct+`[dev_dependencies.helper]
source = 'path'
path = 'helper'
`)

	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		ManifestHash:    man.Hash(),
		Products:        map[string]manifest.LockProduct{"default": {}},
		DevDependencies: []manifest.LockDependency{{
			Name:        "helper",
			Version:     "0.1.0",
			Source:      "path",
			Path:        "helper",
			ContentHash: "sha256:stale",
		}},
	}

	engine := resolve.NewEngine(newFakeAdapter(), projectDir, "tt 3.4.0")

	stale, reason, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.True(t, stale)
	assert.Contains(t, reason, "helper")
}
