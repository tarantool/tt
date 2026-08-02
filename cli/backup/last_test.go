package backup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup/storage"
)

func TestLatestManifestReadsOnlyLastObject(t *testing.T) {
	ctx := context.Background()
	cfg, err := ParseStorageURI("file://" + t.TempDir())
	require.NoError(t, err)
	store, err := OpenStorage(cfg)
	require.NoError(t, err)

	// An older broken manifest must not affect reading the latest one.
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey("0000-broken"),
		[]byte("{"),
	))

	want := ClusterManifest{
		SchemaVersion: SchemaVersion,
		BackupID:      "2026-01-01-full",
	}
	data, err := json.Marshal(want)
	require.NoError(t, err)
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey(string(want.BackupID)),
		data,
	))

	got, err := LatestManifest(ctx, store)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.BackupID, got.BackupID)
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
