package backup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup/storage"
)

// tempStorage opens an empty file-backed storage.
func tempStorage(t *testing.T) storage.Storage {
	t.Helper()

	cfg, err := ParseStorageURI("file://" + t.TempDir())
	require.NoError(t, err)
	store, err := OpenStorage(cfg)
	require.NoError(t, err)
	return store
}

// validManifest returns a manifest that passes Validate(), stamped with the
// given backup id.
func validManifest(t *testing.T, backupID string) ClusterManifest {
	t.Helper()

	manifest := mustDecodeClusterManifest(t, fixtureClusterManifest)
	manifest.BackupID = BackupID(backupID)
	manifest.BaseFullBackupID = BackupID(backupID)
	require.NoError(t, manifest.Validate())
	return manifest
}

// putManifestAt stores a manifest under an arbitrary key.
func putManifestAt(t *testing.T, store storage.Storage, key string, manifest ClusterManifest) {
	t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, storage.PutBytes(context.Background(), store, key, data))
}

// putManifest stores a manifest under the key of its own backup id.
func putManifest(t *testing.T, store storage.Storage, manifest ClusterManifest) {
	t.Helper()

	putManifestAt(t, store, storage.ManifestKey(string(manifest.BackupID)), manifest)
}

func TestLatestManifestReadsOnlyLastObject(t *testing.T) {
	ctx := context.Background()
	store := tempStorage(t)

	// An older broken manifest must not affect reading the latest one.
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey("0000-broken"),
		[]byte("{"),
	))

	want := validManifest(t, "2026-01-01-full")
	putManifest(t, store, want)

	got, err := LatestManifest(ctx, store)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.BackupID, got.BackupID)
}

// TestLatestManifestRejectsUnusableManifest checks a manifest that decodes to
// a zero value is not reported as the latest backup: an orchestrator reading
// backup_id from it chains the next increment onto "".
func TestLatestManifestRejectsUnusableManifest(t *testing.T) {
	for _, body := range []string{"null", "{}"} {
		t.Run(body, func(t *testing.T) {
			ctx := context.Background()
			store := tempStorage(t)

			putManifest(t, store, validManifest(t, "2026-01-01-full"))
			require.NoError(t, storage.PutBytes(
				ctx,
				store,
				storage.ManifestKey("2026-02-01-null"),
				[]byte(body),
			))

			_, err := LatestManifest(ctx, store)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "manifests/2026-02-01-null.json")
		})
	}
}

// TestLatestManifestSkipsNonManifestObjects checks an object that is not a
// manifest does not take the command out, whatever it sorts next to.
func TestLatestManifestSkipsNonManifestObjects(t *testing.T) {
	ctx := context.Background()
	store := tempStorage(t)

	want := validManifest(t, "2026-01-01-full")
	putManifest(t, store, want)
	for _, key := range []string{
		"manifests/zz-README.txt",
		"manifests/staging/zzz.bin",
		"manifests/.DS_Store",
	} {
		require.NoError(t, storage.PutBytes(ctx, store, key, []byte("not a manifest")))
	}

	got, err := LatestManifest(ctx, store)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.BackupID, got.BackupID)
}

// TestLatestManifestRejectsKeyBackupIDMismatch checks a key that does not
// match the backup id it holds is reported: "latest by key" and chain.Latest's
// "latest by backup_id" answer differently on such a storage.
func TestLatestManifestRejectsKeyBackupIDMismatch(t *testing.T) {
	ctx := context.Background()
	store := tempStorage(t)

	putManifest(t, store, validManifest(t, "2026-01-02-inc"))
	putManifestAt(t, store, "manifests/9999-zzz.json", validManifest(t, "2026-01-01-full"))

	_, err := LatestManifest(ctx, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifests/9999-zzz.json")
	assert.Contains(t, err.Error(), "2026-01-01-full")
}

func TestLatestManifestEmptyStorage(t *testing.T) {
	cfg, err := ParseStorageURI("file://" + t.TempDir())
	require.NoError(t, err)
	store, err := OpenStorage(cfg)
	require.NoError(t, err)

	manifest, err := LatestManifest(context.Background(), store)
	require.NoError(t, err)
	assert.Nil(t, manifest)
}

func TestLatestManifestCorruptLatest(t *testing.T) {
	ctx := context.Background()
	cfg, err := ParseStorageURI("file://" + t.TempDir())
	require.NoError(t, err)
	store, err := OpenStorage(cfg)
	require.NoError(t, err)

	// A valid older manifest.
	valid := ClusterManifest{
		SchemaVersion: SchemaVersion,
		BackupID:      "2026-01-01-full",
	}
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey(string(valid.BackupID)),
		data,
	))

	// A corrupt latest manifest.
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey("2026-01-02-broken"),
		[]byte("{invalid"),
	))

	_, err = LatestManifest(ctx, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode latest backup manifest")
}

func TestOpenStorage_UnknownType(t *testing.T) {
	_, err := OpenStorage(&StorageConfig{Type: "unknown"})
	require.Error(t, err)
}
