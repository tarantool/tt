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

// implicitLuatest is the requirement a test runner adds when the project
// declares none: whatever the registry serves.
var implicitLuatest = map[string]manifest.Dependency{
	"luatest": {Source: "registry", Version: "*"},
}

// TestResolveDevExtraAddsAnUndeclaredRequirement is the base case: a manifest
// with no [dev_dependencies] at all still gets a closure for the extra
// requirement, transitives included.
func TestResolveDevExtraAddsAnUndeclaredRequirement(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "aaa").
		add("luatest", "1.0.1-1", "bbb", dep(t, "checks", ">=3.0")).
		add("checks", "3.1.0-1", "ccc")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	require.Nil(t, lock.DevDependencies)

	closure, warnings, err := engine.ResolveDevExtra(
		context.Background(), man, lock, implicitLuatest)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, []string{"checks", "luatest"}, depNames(closure))
}

// TestResolveDevExtraHoldsTheLocksPicks is why the lock is passed in at all:
// the extra closure lands in the same .rocks/ tree as the products, which
// cannot hold two versions of a rock. Without the pin the newer common would
// win here.
func TestResolveDevExtraHoldsTheLocksPicks(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("common", "1.5.0-1", "aaa").
		add("common", "2.0.0-1", "bbb").
		add("luatest", "1.0.1-1", "ccc", dep(t, "common", ">=1.0.0"))

	man := parseManifest(t, oneProduct+`[dependencies]
common = '==1.5.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	require.Equal(t, "1.5.0-1", findDep(t, lock.Products["default"].Dependencies, "common").Version)

	closure, _, err := engine.ResolveDevExtra(
		context.Background(), man, lock, implicitLuatest)
	require.NoError(t, err)
	assert.Equal(t, "1.5.0-1", findDep(t, closure, "common").Version)
}

// TestResolveDevExtraKeepsADeclaredConstraint is the "in addition to, not
// instead of" rule: a project that pins its own runner keeps that pin, and the
// implicit "*" neither widens it nor makes the dependency multiply-declared.
func TestResolveDevExtraKeepsADeclaredConstraint(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("luatest", "0.5.7-1", "aaa").
		add("luatest", "1.0.1-1", "bbb")

	man := parseManifest(t, oneProduct+`[dev_dependencies]
luatest = '==0.5.7'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	closure, _, err := engine.ResolveDevExtra(
		context.Background(), man, nil, implicitLuatest)
	require.NoError(t, err)
	assert.Equal(t, "0.5.7-1", findDep(t, closure, "luatest").Version)
}

// TestResolveDevExtraKeepsTheDeclaredDevClosure pins that the extra requirement
// widens the dev set rather than replacing it: what the manifest declares is
// resolved too, so materializing the result cannot drop a dev rock the project
// asked for.
func TestResolveDevExtraKeepsTheDeclaredDevClosure(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("luacov", "0.15.0-1", "aaa").
		add("luatest", "1.0.1-1", "bbb")

	man := parseManifest(t, oneProduct+`[dev_dependencies]
luacov = '>=0.15.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	closure, _, err := engine.ResolveDevExtra(
		context.Background(), man, nil, implicitLuatest)
	require.NoError(t, err)
	assert.Equal(t, []string{"luacov", "luatest"}, depNames(closure))
}

// TestResolveDevExtraWithNothingToAddIsEmpty covers the degenerate call: no
// declared dev dependencies and no extra ones resolve to no closure, so a
// caller can pass an empty set without special-casing it.
func TestResolveDevExtraWithNothingToAddIsEmpty(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct)
	engine := resolve.NewEngine(newFakeAdapter(), "", "tt 3.4.0")

	closure, warnings, err := engine.ResolveDevExtra(context.Background(), man, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Empty(t, closure)
}

// TestResolveDevExtraLeavesTheProjectFilesAlone is the constraint that makes an
// implicit requirement safe to add: resolving one must not touch
// app.manifest.toml or app.manifest.lock. A byte comparison is the assertion
// because the manifest hash is derived from those bytes, so any rewrite - even
// a semantically identical one - would make the lock stale and turn running the
// tests into an edit of the project.
func TestResolveDevExtraLeavesTheProjectFilesAlone(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "aaa").
		add("luatest", "1.0.1-1", "bbb")

	projectDir := t.TempDir()
	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, projectDir, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	lockBytes, err := lock.Marshal()
	require.NoError(t, err)

	manifestPath := filepath.Join(projectDir, "app.manifest.toml")
	lockPath := filepath.Join(projectDir, "app.manifest.lock")
	manifestBytes := []byte(oneProduct + "[dependencies]\nmetrics = '>=1.0.0'\n")

	require.NoError(t, os.WriteFile(manifestPath, manifestBytes, 0o600))
	require.NoError(t, os.WriteFile(lockPath, lockBytes, 0o600))

	_, _, err = engine.ResolveDevExtra(context.Background(), man, lock, implicitLuatest)
	require.NoError(t, err)

	afterManifest, err := os.ReadFile(manifestPath) //nolint:gosec // Test reads a temp path.
	require.NoError(t, err)
	assert.Equal(t, manifestBytes, afterManifest)

	afterLock, err := os.ReadFile(lockPath) //nolint:gosec // Test reads a temp path.
	require.NoError(t, err)
	assert.Equal(t, lockBytes, afterLock)

	// The in-memory lock is the other copy a caller could accidentally mutate,
	// and it is what a later write would put on disk.
	assert.Nil(t, lock.DevDependencies)
}
