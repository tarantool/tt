package fs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/lib/backup/storage"
)

func TestPut(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	s := newTestStorage(t, root)

	key := storage.ArchiveKey("put", "rs1")
	data := []byte("archive")
	require.NoError(t, s.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	require.FileExists(t, filepath.Join(root, "cluster", "production", key))
}

func TestPutWithoutPrefix(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()

	s, err := New(Config{Path: root})
	require.NoError(t, err)

	key := storage.ManifestKey("no-prefix")
	data := []byte(`{"ok":true}`)
	require.NoError(t, s.Put(ctx, key, bytes.NewReader(data), int64(len(data))))
	require.FileExists(t, filepath.Join(root, key))
}

func TestPutCancelledContext(t *testing.T) {
	root := t.TempDir()
	s := newTestStorage(t, root)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	key := storage.ManifestKey("cancelled")
	err := s.Put(ctx, key, bytes.NewReader([]byte("data")), 4)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestGet(t *testing.T) {
	ctx := t.Context()
	s := newTestStorage(t, t.TempDir())

	key := storage.ManifestKey("get")
	data := []byte(`{"ok":true}`)
	require.NoError(t, s.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	reader, err := s.Get(ctx, key)
	require.NoError(t, err)
	defer reader.Close()

	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, data, actual)
}

func TestGetNotFound(t *testing.T) {
	s := newTestStorage(t, t.TempDir())

	_, err := s.Get(t.Context(), storage.ManifestKey("missing"))
	require.True(t, errors.Is(err, storage.ErrKeyNotFound))
}

func TestList(t *testing.T) {
	ctx := t.Context()
	s := newTestStorage(t, t.TempDir())

	manifestKey := storage.ManifestKey("list")
	archiveKey := storage.ArchiveKey("list", "rs1")
	manifest := []byte(`{"ok":true}`)
	archive := []byte("archive")
	require.NoError(t, s.Put(ctx, manifestKey, bytes.NewReader(manifest), int64(len(manifest))))
	require.NoError(t, s.Put(ctx, archiveKey, bytes.NewReader(archive), int64(len(archive))))

	objects, err := s.List(ctx, storage.ManifestsPrefix())
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, manifestKey, objects[0].Key)
	require.Equal(t, int64(len(manifest)), objects[0].Size)
	require.False(t, objects[0].LastModified.IsZero())
}

func TestListEmpty(t *testing.T) {
	s := newTestStorage(t, t.TempDir())

	objects, err := s.List(t.Context(), storage.ManifestsPrefix())
	require.NoError(t, err)
	require.Empty(t, objects)
}

func TestListWithObjectPrefix(t *testing.T) {
	ctx := t.Context()
	s := newTestStorage(t, t.TempDir())

	matchingKey := storage.ArchiveKey("backup", "rs1")
	otherKey := storage.ArchiveKey("other", "rs1")
	require.NoError(
		t,
		s.Put(ctx, matchingKey, bytes.NewReader([]byte("matching")), int64(len("matching"))),
	)
	require.NoError(t, s.Put(ctx, otherKey, bytes.NewReader([]byte("other")), int64(len("other"))))

	objects, err := s.List(ctx, "data/backup")
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, matchingKey, objects[0].Key)
}

func TestDelete(t *testing.T) {
	ctx := t.Context()
	s := newTestStorage(t, t.TempDir())

	key := storage.ManifestKey("delete")
	data := []byte(`{"ok":true}`)
	require.NoError(t, s.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	require.NoError(t, s.Delete(ctx, key))
	_, err := s.Get(ctx, key)
	require.True(t, errors.Is(err, storage.ErrKeyNotFound))
}

func TestDeleteNotFound(t *testing.T) {
	s := newTestStorage(t, t.TempDir())

	err := s.Delete(t.Context(), storage.ManifestKey("missing"))
	require.NoError(t, err)
}

func TestStorageRejectsInvalidKey(t *testing.T) {
	s := newTestStorage(t, t.TempDir())

	err := s.Put(t.Context(), "../escape", nil, 0)
	require.True(t, errors.Is(err, storage.ErrInvalidKey))

	err = s.Put(t.Context(), "data//archive.tar.zst", nil, 0)
	require.True(t, errors.Is(err, storage.ErrInvalidKey))
}

func TestStorageRejectsTempFileKey(t *testing.T) {
	s := newTestStorage(t, t.TempDir())

	err := s.Put(t.Context(), "data/.tt-backup-archive.tar.zst", nil, 0)
	require.True(t, errors.Is(err, storage.ErrInvalidKey))
}

func TestNewRejectsInvalidPrefix(t *testing.T) {
	_, err := New(Config{Path: t.TempDir(), Prefix: "../escape"})
	require.True(t, errors.Is(err, storage.ErrInvalidKey))
}

func TestNewRejectsEmptyPath(t *testing.T) {
	_, err := New(Config{Path: ""})
	require.True(t, errors.Is(err, errPathRequired))
}

func TestConcurrentPut(t *testing.T) {
	ctx := t.Context()
	s := newTestStorage(t, t.TempDir())

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()
			key := storage.ManifestKey("concurrent")
			data := []byte("data")
			_ = s.Put(ctx, key, bytes.NewReader(data), int64(len(data)))
		}()
	}
	wg.Wait()

	reader, err := s.Get(ctx, storage.ManifestKey("concurrent"))
	require.NoError(t, err)
	defer reader.Close()

	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("data"), actual)
}

func newTestStorage(t *testing.T, root string) *Storage {
	t.Helper()

	s, err := New(Config{
		Path:   root,
		Prefix: "cluster/production/",
	})
	require.NoError(t, err)
	return s
}
