package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// sharedDep is the single registry dependency the multi-package tests share.
const sharedDep = "luasocket"

// withRocks is a with-deps archive spec for a package that ships its own files
// and the shared registry dependency, at the given locked version and declared
// constraint.
func withRocks(name, version, depVer, depConstraint string) archiveSpec {
	return archiveSpec{
		name: name, version: version, withRuntime: "3.0.5",
		deps:     map[string]string{sharedDep: depConstraint},
		lockDeps: []LockDep{{Name: sharedDep, Version: depVer, Source: "registry"}},
		files:    mergeFiles(rockFiles(name, version), rockFiles(sharedDep, depVer)),
	}
}

// installInto runs one install into a fresh project directory.
func installInto(t *testing.T, dir string, opts Options, archives ...string) (*Result, error) {
	t.Helper()

	opts.ProjectDir = dir
	opts.Archives = archives
	opts.Yes = true

	return Run(context.Background(), opts)
}

// TestInstallWithDepsOffline covers the headline case: a with-deps archive into
// an empty project is a pure offline extraction — the tree lands and no network
// is touched (no Tarantool facts are provided).
func TestInstallWithDepsOffline(t *testing.T) {
	t.Parallel()

	archive := withRocks("my-app", "1.0.0", "3.0.4", ">=3.0.0").build(t)

	dir := t.TempDir()
	result, err := installInto(t, dir, Options{Scope: ScopeProject}, archive)
	require.NoError(t, err)
	require.Len(t, result.Installed, 1)

	assert.FileExists(t, filepath.Join(dir, ".rocks", "share", "tarantool", "my-app", "init.lua"))
	assert.FileExists(t,
		filepath.Join(dir, ".rocks", "share", "tarantool", "luasocket", "init.lua"))
	assert.FileExists(t, filepath.Join(dir, "_runtime", "tarantool", "bin", "tarantool"))
}

// TestInstallWritesMetadata checks the per-package metadata install records for
// list/uninstall and refcounting.
func TestInstallWritesMetadata(t *testing.T) {
	t.Parallel()

	archive := withRocks("my-app", "1.2.3", "3.0.4", ">=3.0.0").build(t)

	dir := t.TempDir()
	_, err := installInto(t, dir, Options{Scope: ScopeProject}, archive)
	require.NoError(t, err)

	metaDir := filepath.Join(dir, ".rocks", "manifests", "my-app")
	assert.FileExists(t, filepath.Join(metaDir, "manifest.toml"))
	assert.FileExists(t, filepath.Join(metaDir, "lock.toml"))
	assert.Equal(t, "1.2.3\n", readFile(t, filepath.Join(metaDir, "VERSION")))

	lock, err := manifest.ParseLock([]byte(readFile(t, filepath.Join(metaDir, "lock.toml"))))
	require.NoError(t, err)

	pin, ok := lockedVersion(lock, "luasocket")
	require.True(t, ok)
	assert.Equal(t, "3.0.4", pin)
}

// TestInstallWithDepsIntoUserRejected pins the header check: a with-deps archive
// aimed at user or system is exit 1, before anything is written.
func TestInstallWithDepsIntoUserRejected(t *testing.T) {
	t.Parallel()

	archive := withRocks("my-app", "1.0.0", "3.0.4", ">=3.0.0").build(t)

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser}, archive)
	require.ErrorIs(t, err, errWithDepsScope)
	assert.Equal(t, exitStateError, ExitCode(err))
}

// TestInstallCollision covers the name-collision policy: a second install of the
// same package without --force/--upgrade is exit 1; --force reinstalls.
func TestInstallCollision(t *testing.T) {
	t.Parallel()

	archive := withRocks("my-app", "1.0.0", "3.0.4", ">=3.0.0").build(t)
	dir := t.TempDir()

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, archive)
	require.NoError(t, err)

	_, err = installInto(t, dir, Options{Scope: ScopeProject}, archive)
	require.ErrorIs(t, err, errNameCollision)
	assert.Equal(t, exitStateError, ExitCode(err))

	_, err = installInto(t, dir, Options{Scope: ScopeProject, Force: true}, archive)
	require.NoError(t, err, "--force reinstalls over the collision")
}

// TestInstallUpgrade covers --upgrade: it installs only a strictly higher
// version and no-ops otherwise.
func TestInstallUpgrade(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	v1 := withRocks("my-app", "1.0.0", "3.0.4", ">=3.0.0").build(t)
	_, err := installInto(t, dir, Options{Scope: ScopeProject}, v1)
	require.NoError(t, err)

	// Same version under --upgrade: nothing to do.
	result, err := installInto(t, dir, Options{Scope: ScopeProject, Upgrade: true}, v1)
	require.NoError(t, err)
	require.Len(t, result.Installed, 1)
	assert.True(t, result.Installed[0].Skipped)

	// Higher version under --upgrade: installed.
	v2 := withRocks("my-app", "2.0.0", "3.0.4", ">=3.0.0").build(t)

	result, err = installInto(t, dir, Options{Scope: ScopeProject, Upgrade: true}, v2)
	require.NoError(t, err)
	assert.False(t, result.Installed[0].Skipped)
	assert.Equal(t, "2.0.0\n",
		readFile(t, filepath.Join(dir, ".rocks", "manifests", "my-app", "VERSION")))
}

// installedRockVersions lists the version directories present for a rock.
func installedRockVersions(t *testing.T, dir, rock string) []string {
	t.Helper()

	entries, err := os.ReadDir(
		filepath.Join(dir, ".rocks", "share", "tarantool", "rocks", rock))
	require.NoError(t, err)

	var versions []string

	for _, entry := range entries {
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}

	return versions
}

// TestInstallMultiSharedSameVersion covers two packages that lock the shared
// dependency at the same version: it lands exactly once.
func TestInstallMultiSharedSameVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	router := withRocks("router", "1.0.0", "3.0.4", ">=3.0.0").build(t)
	storage := withRocks("storage", "1.0.0", "3.0.4", ">=3.0.0").build(t)

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, router)
	require.NoError(t, err)

	_, err = installInto(t, dir, Options{Scope: ScopeProject}, storage)
	require.NoError(t, err)

	assert.Equal(t, []string{"3.0.4"}, installedRockVersions(t, dir, "luasocket"))
}

// TestInstallMultiSharedReconciled covers two packages whose compatible
// constraints lock the shared dependency at different versions: the higher
// version is reconciled to and the older one is removed.
func TestInstallMultiSharedReconciled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	router := withRocks("router", "1.0.0", "3.0.4", ">=3.0.0").build(t)
	storage := withRocks("storage", "1.0.0", "3.1.0", ">=3.0.0,<4.0.0").build(t)

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, router)
	require.NoError(t, err)

	_, err = installInto(t, dir, Options{Scope: ScopeProject}, storage)
	require.NoError(t, err)

	assert.Equal(t, []string{"3.1.0"}, installedRockVersions(t, dir, "luasocket"),
		"reconciled to the higher version, old one removed")
}

// TestInstallMultiSharedIncompatible covers two packages whose constraints no
// locked version can satisfy: exit 1 with a breakdown.
func TestInstallMultiSharedIncompatible(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	router := withRocks("router", "1.0.0", "3.0.4", "<3.1.0").build(t)
	storage := withRocks("storage", "1.0.0", "3.1.0", ">=3.1.0").build(t)

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, router)
	require.NoError(t, err)

	_, err = installInto(t, dir, Options{Scope: ScopeProject}, storage)
	require.ErrorIs(t, err, errIncompatibleDeps)
	assert.Equal(t, exitStateError, ExitCode(err))
	assert.Contains(t, err.Error(), "router pinned 3.0.4")
}

// TestInstallPartialMultiExit3 covers one invocation over several archives where
// some install and some fail: exit code 3.
func TestInstallPartialMultiExit3(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	router := withRocks("router", "1.0.0", "3.0.4", "<3.1.0").build(t)
	storage := withRocks("storage", "1.0.0", "3.1.0", ">=3.1.0").build(t)

	result, err := installInto(t, dir, Options{Scope: ScopeProject}, router, storage)
	require.Error(t, err)
	assert.Equal(t, exitPartialError, ExitCode(err))
	assert.Len(t, result.Installed, 1)
	assert.Len(t, result.Failed, 1)
	assert.Equal(t, "router", result.Installed[0].Package)
}
