package deps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
)

// TestResolve_holdsEveryLockedVersion is what separates resolve from update:
// the lock is rewritten from the manifest, but the versions it already holds
// are preferred. Without the pins a hand edit to one dependency would drag
// every unrelated one to its newest release.
func TestResolve_holdsEveryLockedVersion(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{
		"checks":    "3.1.0-1",
		"luasocket": "3.0.0-1",
	}))
	res := &fakeResolver{}

	_, err := resolveWith(context.Background(), optsFor(dir), res)
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Equal(t, resolve.Pins{"checks": "3.1.0-1", "luasocket": "3.0.0-1"}, res.pins[0])
}

// TestResolve_leavesTheManifestAlone: resolve changes no declaration, so the
// file the user hand-edited must come back byte for byte — including the
// comment the position-preserving editor exists to protect.
func TestResolve_leavesTheManifestAlone(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	res := &fakeResolver{}

	_, err := resolveWith(context.Background(), optsFor(dir), res)
	require.NoError(t, err)

	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
}

// TestResolve_writesTheLockWithTheManifestHash pins the reason the command
// exists: after it runs the lock matches the manifest on disk, so the very next
// build does not find it stale.
func TestResolve_writesTheLockWithTheManifestHash(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.0.0-1"}))
	res := &fakeResolver{lock: lockOf(map[string]string{"checks": "3.1.0-1"})}

	result, err := resolveWith(context.Background(), optsFor(dir), res)
	require.NoError(t, err)

	man, warnings, err := manifest.ParseManifest([]byte(manifestOnDisk(t, dir)))
	require.NoError(t, err)
	require.Empty(t, warnings)

	assert.Equal(t, man.Hash(), result.Lock.ManifestHash)

	written := lockOnDisk(t, dir)
	assert.Equal(t, man.Hash(), written.ManifestHash)

	stale, reason, err := resolve.IsStale(dir, man, written)
	require.NoError(t, err)
	assert.False(t, stale, reason)
}

// TestResolve_reportsAHashItRefreshed: a resolve that moves no rock but
// rewrites manifest_hash says so, as tt package resolve prints it, while one
// over a lock written for the manifest as it says it now does not - a comment
// added since is no reason.
func TestResolve_reportsAHashItRefreshed(t *testing.T) {
	t.Parallel()

	pins := map[string]string{"checks": "3.1.0-1"}

	man, _, err := manifest.ParseManifest([]byte(baseManifest))
	require.NoError(t, err)

	sum := sha256.Sum256([]byte(baseManifest))
	older := "sha256:" + hex.EncodeToString(sum[:])
	refreshed := []string{"manifest hash refreshed"}

	cases := []struct {
		name     string
		onDisk   string
		hash     string
		rewrites []string
	}{
		{"the hash of the manifest", baseManifest, man.Hash(), nil},
		{"the hash before a comment", "# edited\n" + baseManifest, man.Hash(), nil},
		{"the hash of another manifest", baseManifest, staleHash, refreshed},
		{"the hash of an older tt", baseManifest, older, refreshed},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			before := lockOf(pins)

			before.ManifestHash = testCase.hash

			dir := writeProject(t, testCase.onDisk, before)

			result, err := resolveWith(context.Background(), optsFor(dir),
				&fakeResolver{lock: lockOf(pins)})
			require.NoError(t, err)

			assert.Empty(t, result.Moves)
			assert.Equal(t, testCase.rewrites, result.Rewrites)
			assert.Equal(t, man.Hash(), lockOnDisk(t, dir).ManifestHash)
		})
	}
}

// TestResolve_reportsAPathDependencyItRepinned: a path dependency whose files
// changed is pinned anew with its version held, which moves nothing, and the
// resolve says so instead of finding the lock up to date.
func TestResolve_reportsAPathDependencyItRepinned(t *testing.T) {
	t.Parallel()

	man, _, err := manifest.ParseManifest([]byte(baseManifest))
	require.NoError(t, err)

	pathLock := func(contentHash string) *manifest.Lock {
		lock := lockOf(map[string]string{"checks": "3.1.0-1"})

		lock.ManifestHash = man.Hash()
		lock.Products["default"] = manifest.LockProduct{Dependencies: append(
			lock.Products["default"].Dependencies, manifest.LockDependency{
				Name: "vendored", Version: "scm-1", Source: "path",
				Path: "vendor/vendored", ContentHash: contentHash,
			})}

		return lock
	}

	dir := writeProject(t, baseManifest, pathLock("sha256:before"))

	result, err := resolveWith(context.Background(), optsFor(dir),
		&fakeResolver{lock: pathLock("sha256:after")})
	require.NoError(t, err)

	assert.Empty(t, result.Moves)
	assert.Equal(t, []string{`path dependency "vendored" re-pinned: its files changed`},
		result.Rewrites)
}

// TestResolve_reportsWhatMoved: the lock is the command's whole output, so the
// closure changes are what it has to report.
func TestResolve_reportsWhatMoved(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.0.0-1"}))
	res := &fakeResolver{lock: lockOf(map[string]string{"checks": "3.1.0-1"})}

	result, err := resolveWith(context.Background(), optsFor(dir), res)
	require.NoError(t, err)

	assert.Equal(t, []Move{{Name: "checks", From: "3.0.0-1", To: "3.1.0-1"}}, result.Moves)
	assert.Equal(t, manifest.Change{}, result.Change)
}

// TestResolve_withoutALockPinsNothing covers the first resolution of a project
// that has never been built: nothing to hold, and that is a normal state.
func TestResolve_withoutALockPinsNothing(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{lock: lockOf(map[string]string{"checks": "3.1.0-1"})}

	result, err := resolveWith(context.Background(), optsFor(dir), res)
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Empty(t, res.pins[0])
	assert.Equal(t, []Move{{Name: "checks", From: "", To: "3.1.0-1"}}, result.Moves)
	assert.Equal(t, "3.1.0-1",
		lockOnDisk(t, dir).Products["default"].Dependencies[0].Version)
}
