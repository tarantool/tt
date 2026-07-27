package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/state"
)

// TestPackagesEmptyTree pins that a scope with nothing installed reads as an
// empty inventory rather than an error: a fresh project has no manifests/ at
// all, and that is not a failure.
func TestPackagesEmptyTree(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	assert.Empty(t, packages)
}

// TestPackagesListsPrimaryAndGuests covers the shape list reads: the primary
// package first, then guests in name order.
func TestPackagesListsPrimaryAndGuests(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writePrimary(t, lay, "my-app", "1.2.3",
		map[string]string{"luasocket": ">=3.0.0"},
		map[string]string{"luasocket": "3.0.4"})
	writeGuest(t, lay, "monitoring", "2.0.0", nil, map[string]string{"metrics": "1.0.0"})
	writeGuest(t, lay, "alerting", "0.5.0", nil, nil)

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	require.Len(t, packages, 3)

	assert.Equal(t, "my-app", packages[0].Name)
	assert.True(t, packages[0].Primary)
	assert.Equal(t, "1.2.3", packages[0].Version)

	// Guests follow in directory order, which os.ReadDir sorts by name.
	assert.Equal(t, "alerting", packages[1].Name)
	assert.False(t, packages[1].Primary)
	assert.Equal(t, "0.5.0", packages[1].Version)

	assert.Equal(t, "monitoring", packages[2].Name)
	assert.Equal(t, "2.0.0", packages[2].Version)
}

// TestPackagesSkipsPrimaryOutsideProjectScope pins that user and system trees
// hold guests only: a stray app.manifest.toml at the root of a shared tree is
// not the project's primary package.
func TestPackagesSkipsPrimaryOutsideProjectScope(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writePrimary(t, lay, "shared-tree-app", "1.2.3", nil, nil)
	writeGuest(t, lay, "some-lib", "2.0.0", nil, nil)

	packages, err := state.Packages(lay, state.ScopeUser)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	assert.Equal(t, "some-lib", packages[0].Name)
}

// TestPackagesSkipsUnreadableGuest pins the robustness rule: a half-written or
// corrupt metadata directory is skipped, not fatal, so one bad install cannot
// make the whole inventory unreadable.
func TestPackagesSkipsUnreadableGuest(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeGuest(t, lay, "good", "1.0.0", nil, nil)

	// A directory with no manifest at all.
	require.NoError(t, os.MkdirAll(filepath.Join(lay.Manifests, "empty"), 0o750))

	// A directory whose manifest is not parseable TOML.
	writeFile(t, filepath.Join(lay.Manifests, "broken", state.MetaManifestFile),
		[]byte("this is not = valid toml ["))

	// A stray regular file where a package directory would be.
	writeFile(t, filepath.Join(lay.Manifests, "stray.txt"), []byte("junk"))

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	assert.Equal(t, "good", packages[0].Name)
}

// TestPackagesTolerantOfMissingLock pins that a guest without a lock still
// lists: it simply owns no dependencies.
func TestPackagesTolerantOfMissingLock(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeGuest(t, lay, "some-lib", "1.0.0", nil, nil)
	require.NoError(t, os.Remove(
		filepath.Join(lay.Manifests, "some-lib", state.MetaLockFile)))

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	assert.Nil(t, packages[0].Lock)
	assert.Empty(t, state.LockedDependencies(packages[0].Lock))
}

// TestPackagesNameComesFromManifest pins that the package's identity is the
// name inside its manifest, not the directory it happens to sit in.
func TestPackagesNameComesFromManifest(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	source := manifestTOML("real-name", nil)
	writeFile(t, filepath.Join(lay.Manifests, "dir-name", state.MetaManifestFile),
		[]byte(source))

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	assert.Equal(t, "real-name", packages[0].Name)
}

// TestGuestManifestCarriesDeclaredConstraints pins that a guest's metadata
// preserves what its manifest declared, not just what its lock pinned. Install's
// reconciliation reads those constraints back off disk when a later package
// wants the same dependency at another version.
func TestGuestManifestCarriesDeclaredConstraints(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeGuest(t, lay, "monitoring", "2.0.0",
		map[string]string{"metrics": ">=1.0.0,<2.0.0"},
		map[string]string{"metrics": "1.4.0"})

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	require.NotNil(t, packages[0].Manifest)

	assert.Equal(t, ">=1.0.0,<2.0.0",
		packages[0].Manifest.Dependencies["metrics"].Version)
}

// TestPrimaryWithoutVersionFile pins that a project with no VERSION pin still
// lists, with an empty version — the inventory does not shell out to git to
// invent one.
func TestPrimaryWithoutVersionFile(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writePrimary(t, lay, "my-app", "", nil, nil)

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	assert.True(t, packages[0].Primary)
	assert.Empty(t, packages[0].Version)
}

// TestFind covers lookup by name and the miss.
func TestFind(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeGuest(t, lay, "monitoring", "2.0.0", nil, nil)

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)

	found, ok := state.Find(packages, "monitoring")
	require.True(t, ok)
	assert.Equal(t, "2.0.0", found.Version)

	_, ok = state.Find(packages, "absent")
	assert.False(t, ok)
}

// TestFindPrimary covers the primary lookup and that a tree of guests alone has
// none.
func TestFindPrimary(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeGuest(t, lay, "monitoring", "2.0.0", nil, nil)

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)

	_, ok := state.FindPrimary(packages)
	assert.False(t, ok)

	writePrimary(t, lay, "my-app", "1.0.0", nil, nil)

	packages, err = state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)

	primary, ok := state.FindPrimary(packages)
	require.True(t, ok)
	assert.Equal(t, "my-app", primary.Name)
}

// TestLockedDependencies pins the ledger's input: every dependency a lock
// pins, keyed by name.
func TestLockedDependencies(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeGuest(t, lay, "monitoring", "2.0.0", nil,
		map[string]string{"metrics": "1.0.0", "checks": "3.1.0"})

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	require.Len(t, packages, 1)

	assert.Equal(t,
		map[string]string{"metrics": "1.0.0", "checks": "3.1.0"},
		state.LockedDependencies(packages[0].Lock))

	pin, ok := state.LockedVersion(packages[0].Lock, "metrics")
	require.True(t, ok)
	assert.Equal(t, "1.0.0", pin)

	_, ok = state.LockedVersion(packages[0].Lock, "absent")
	assert.False(t, ok)

	// A nil lock is the "predates a lock-writing tt" case and must not panic.
	_, ok = state.LockedVersion(nil, "metrics")
	assert.False(t, ok)
	assert.Empty(t, state.LockedDependencies(nil))
}

// TestWriteAndRemoveMetadata covers the round trip the ledger stands on:
// metadata written by an install reads back, and removing it drops the package
// from the inventory entirely.
func TestWriteAndRemoveMetadata(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeGuest(t, lay, "monitoring", "2.0.0", nil, map[string]string{"metrics": "1.0.0"})

	metaDir := filepath.Join(lay.Manifests, "monitoring")
	assert.FileExists(t, filepath.Join(metaDir, state.MetaManifestFile))
	assert.FileExists(t, filepath.Join(metaDir, state.MetaLockFile))
	assert.Equal(t, "2.0.0\n", string(mustRead(t, filepath.Join(metaDir, state.MetaVersionFile))))

	require.NoError(t, state.RemoveMetadata(lay, "monitoring"))
	assert.NoDirExists(t, metaDir)

	packages, err := state.Packages(lay, state.ScopeProject)
	require.NoError(t, err)
	assert.Empty(t, packages)
}

// TestRemoveMetadataAbsentIsNoOp pins that unwinding a package twice is not an
// error, so a partially failed uninstall can be re-run.
func TestRemoveMetadataAbsentIsNoOp(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	require.NoError(t, state.RemoveMetadata(lay, "never-installed"))
}

// mustRead reads a file or fails the test.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // Test reads from a temp path.
	require.NoError(t, err)

	return data
}
