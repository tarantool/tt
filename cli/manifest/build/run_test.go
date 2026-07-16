package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/rocks"
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
	data, err := os.ReadFile(vluaPath) //nolint:gosec // temp path
	require.NoError(t, err)
	assert.Contains(t, string(data), `version  = "1.2.3"`)
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
	assert.Equal(t, exitStateError, ExitCode(err))
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
	assert.Equal(t, exitStateError, ExitCode(err))
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
	so := filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so")
	vlua := filepath.Join(tree, "share/tarantool/my-app/version.lua")
	require.NoError(t, os.Remove(so))
	require.NoError(t, os.Remove(vlua))

	// Fetch runs materialization only; with no deps it neither rebuilds the
	// native artifact nor regenerates version.lua.
	opts := dryOptions(dir)
	opts.FetchOnly = true
	require.NoError(t, Run(context.Background(), opts))

	assert.False(t, exists(t, so))
	assert.False(t, exists(t, vlua))
}

func TestRun_fetchWithoutLockFails(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{"init.lua": "-- init"})

	opts := dryOptions(dir)
	opts.FetchOnly = true
	err := Run(context.Background(), opts)
	require.Error(t, err)
	assert.Equal(t, exitStateError, ExitCode(err))
}
