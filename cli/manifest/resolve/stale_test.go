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

func TestStaleOnManifestEdit(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().add("metrics", "1.0.0-1", "a")

	body := oneProduct + `[dependencies]
metrics = '>=1.0.0'
`
	man := parseManifest(t, body)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// The same manifest is not stale.
	stale, _, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.False(t, stale)

	// Any byte change - even a comment - re-hashes the manifest and staleness fires.
	edited := parseManifest(t, body+"# touched\n")

	stale, reason, err := engine.IsStale(edited, lock)
	require.NoError(t, err)
	assert.True(t, stale)
	assert.Contains(t, reason, "manifest changed")
}

func TestNotStaleOnNewRegistryVersion(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().add("metrics", "1.0.0-1", "a")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// A newer version appearing in the registry must not make the lock stale;
	// only tt package update pulls it. Staleness never consults the registry.
	fake.add("metrics", "2.0.0-1", "b")

	stale, _, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.False(t, stale)
}

func TestStaleOnPathContentChange(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	vendor := filepath.Join(projectDir, "vendor", "mymod")
	require.NoError(t, os.MkdirAll(vendor, 0o750))

	initLua := filepath.Join(vendor, "init.lua")
	require.NoError(t, os.WriteFile(initLua, []byte("return {}\n"), 0o600))

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
[dependencies.mymod]
source = 'path'
path = 'vendor/mymod'
[components.app]
path = '.'
[products.default]
components = ['app']
default = true
`)

	engine := resolve.NewEngine(newFakeAdapter(), projectDir, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	dependency := findDep(t, lock.Products["default"].Dependencies, "mymod")
	assert.Equal(t, "path", dependency.Source)
	assert.Equal(t, "vendor/mymod", dependency.Path)
	assert.NotEmpty(t, dependency.ContentHash)

	// Unchanged tree: not stale.
	stale, _, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.False(t, stale)

	// Change local content: stale.
	changed := []byte("return {changed=true}\n")
	require.NoError(t, os.WriteFile(initLua, changed, 0o600))

	stale, reason, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.True(t, stale)
	assert.Contains(t, reason, "mymod")
}
