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
)

// fakeResolver drives the lock gate without a registry.
type fakeResolver struct {
	stale         bool
	reason        string
	fresh         *manifest.Lock
	resolveCalled bool
}

func (f *fakeResolver) IsStale(*manifest.Manifest, *manifest.Lock) (bool, string, error) {
	return f.stale, f.reason, nil
}

func (f *fakeResolver) Resolve(
	context.Context, *manifest.Manifest,
) (*manifest.Lock, []string, error) {
	f.resolveCalled = true
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
