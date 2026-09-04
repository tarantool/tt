package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
)

// fakeResolver drives the lock gate without a registry.
type fakeResolver struct {
	stale         bool
	reason        string
	fresh         *manifest.Lock
	resolveCalled bool
	// pins records the pin set of every resolve, in order.
	pins []resolve.Pins
}

func (f *fakeResolver) IsStale(*manifest.Manifest, *manifest.Lock) (bool, string, error) {
	return f.stale, f.reason, nil
}

func (f *fakeResolver) ResolvePinned(
	_ context.Context, _ *manifest.Manifest, pins resolve.Pins,
) (*manifest.Lock, []string, error) {
	f.resolveCalled = true
	f.pins = append(f.pins, pins)

	return f.fresh, nil, nil
}

// freshLock is a minimal, round-trippable lock the fake resolver returns.
func freshLock() *manifest.Lock {
	return &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: "0.1",
		GeneratedBy:     "tt test",
		ManifestHash:    "sha256:new",
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
			}},
		},
	}
}

// writeLock marshals lock into projectDir.
func writeLock(t *testing.T, projectDir string, lock *manifest.Lock) {
	t.Helper()
	data, err := lock.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, lockFileName), data, 0o600))
}

func lockOnDisk(t *testing.T, projectDir string) *manifest.Lock {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectDir, lockFileName)) //nolint:gosec // temp path
	require.NoError(t, err)
	lock, err := manifest.ParseLock(data)
	require.NoError(t, err)
	return lock
}

func TestGateLock_noLockResolvesAndWrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := &fakeResolver{fresh: freshLock()}

	lock, _, err := gateLock(context.Background(), r, &manifest.Manifest{}, dir, false)
	require.NoError(t, err)
	assert.True(t, r.resolveCalled)
	assert.Contains(t, lock.Products, "default")
	// The freshly resolved lock is persisted.
	assert.Equal(t, "sha256:new", lockOnDisk(t, dir).ManifestHash)
}

func TestGateLock_noLockUnderLockedFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := &fakeResolver{fresh: freshLock()}

	_, _, err := gateLock(context.Background(), r, &manifest.Manifest{}, dir, true)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errLockStale))
	assert.Equal(t, exitStateError, ExitCode(err))
	assert.False(t, r.resolveCalled)
}

func TestGateLock_freshLockReused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := freshLock()
	existing.ManifestHash = "sha256:existing"
	writeLock(t, dir, existing)

	r := &fakeResolver{stale: false}
	lock, _, err := gateLock(context.Background(), r, &manifest.Manifest{}, dir, true)
	require.NoError(t, err)
	assert.False(t, r.resolveCalled)
	assert.Equal(t, "sha256:existing", lock.ManifestHash)
}

func TestGateLock_staleUnlockedRewrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := freshLock()
	existing.ManifestHash = "sha256:existing"
	writeLock(t, dir, existing)

	r := &fakeResolver{stale: true, reason: "manifest changed", fresh: freshLock()}
	_, _, err := gateLock(context.Background(), r, &manifest.Manifest{}, dir, false)
	require.NoError(t, err)
	assert.True(t, r.resolveCalled)
	assert.Equal(t, "sha256:new", lockOnDisk(t, dir).ManifestHash)
}

// TestGateLock_staleRewriteHoldsLockedVersions pins what separates a build's
// re-resolve from an update: a stale lock still decides the versions, so a
// build never pulls a newer release than the one already recorded.
func TestGateLock_staleRewriteHoldsLockedVersions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := freshLock()
	existing.ManifestHash = "sha256:existing"
	existing.Products["default"] = manifest.LockProduct{
		Dependencies: []manifest.LockDependency{
			{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
			{Name: "metrics", Version: "1.0.0-1", Source: sourceRegistry},
		},
	}
	writeLock(t, dir, existing)

	r := &fakeResolver{stale: true, reason: "manifest changed", fresh: freshLock()}
	_, _, err := gateLock(context.Background(), r, &manifest.Manifest{}, dir, false)
	require.NoError(t, err)

	require.Len(t, r.pins, 1)
	assert.Equal(t, resolve.Pins{"checks": "3.1.0-1", "metrics": "1.0.0-1"}, r.pins[0])
}

// TestGateLock_noLockPinsNothing covers the first resolve of a project: there
// is no recorded version to hold, so the registry decides every pick.
func TestGateLock_noLockPinsNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	r := &fakeResolver{stale: false, reason: "", fresh: freshLock()}
	_, _, err := gateLock(context.Background(), r, &manifest.Manifest{}, dir, false)
	require.NoError(t, err)

	require.Len(t, r.pins, 1)
	assert.Empty(t, r.pins[0])
}

func TestGateLock_staleLockedFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLock(t, dir, freshLock())

	r := &fakeResolver{stale: true, reason: "manifest changed"}
	_, _, err := gateLock(context.Background(), r, &manifest.Manifest{}, dir, true)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errLockStale))
	assert.Contains(t, err.Error(), "manifest changed")
	assert.False(t, r.resolveCalled)
}

func TestLoadLock_missingIsError(t *testing.T) {
	t.Parallel()

	_, err := loadLock(t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, errNoLock))
}
