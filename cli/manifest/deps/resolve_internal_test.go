package deps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
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
