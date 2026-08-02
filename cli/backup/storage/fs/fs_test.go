package fs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup/storage"
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

func TestGetDirectoryKeyNotFound(t *testing.T) {
	ctx := t.Context()
	s := newTestStorage(t, t.TempDir())

	// Store an object so that its parent ("data") exists as a directory, then
	// request that directory as a key: it must read as missing, not EISDIR.
	require.NoError(t, s.Put(ctx, storage.ArchiveKey("dir", "rs1"),
		bytes.NewReader([]byte("archive")), int64(len("archive"))))

	_, err := s.Get(ctx, "data")
	require.True(t, errors.Is(err, storage.ErrKeyNotFound))
}

func TestPutRejectsNegativeSize(t *testing.T) {
	s := newTestStorage(t, t.TempDir())

	err := s.Put(t.Context(), storage.ManifestKey("neg"), bytes.NewReader([]byte("x")), -1)
	require.True(t, errors.Is(err, errNegativeSize))
}

func TestPutRejectsSizeMismatch(t *testing.T) {
	ctx := t.Context()
	s := newTestStorage(t, t.TempDir())

	key := storage.ManifestKey("mismatch")
	err := s.Put(ctx, key, bytes.NewReader([]byte("four")), 100)
	require.Error(t, err)

	// The failed Put must not leave a partial object behind.
	_, err = s.Get(ctx, key)
	require.True(t, errors.Is(err, storage.ErrKeyNotFound))
}

func TestPutStoresReadableMode(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	s := newTestStorage(t, root)

	key := storage.ManifestKey("mode")
	require.NoError(t, s.Put(ctx, key, bytes.NewReader([]byte("x")), 1))

	info, err := os.Stat(filepath.Join(root, "cluster", "production", key))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
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

// A storage the operator named must exist. A typo in --backup-storage reported
// as an empty listing tells a cron job "the daemon never ran", and tt backup
// plan --target=incremental answers that with "use --target=full", i.e. reset
// the chain against the wrong path.
func TestListMissingRootIsError(t *testing.T) {
	s, err := New(Config{Path: filepath.Join(t.TempDir(), "typo")})
	require.NoError(t, err)

	_, err = s.List(t.Context(), storage.ManifestsPrefix())
	require.ErrorIs(t, err, errRootNotFound)
}

func TestListRootIsFileIsError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "regular")
	require.NoError(t, os.WriteFile(root, []byte("not a storage"), 0o644))

	s, err := New(Config{Path: root})
	require.NoError(t, err)

	_, err = s.List(t.Context(), storage.ManifestsPrefix())
	require.ErrorIs(t, err, errNotDirectory)
}

// A file where manifests/ belongs is walked as the file itself, and the key
// "manifests" fails the "manifests/" prefix test - which used to leave an empty
// listing indistinguishable from a storage holding no backups.
func TestListPrefixShadowedByFileIsError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifests"), []byte("x"), 0o644))

	s, err := New(Config{Path: root})
	require.NoError(t, err)

	_, err = s.List(t.Context(), storage.ManifestsPrefix())
	require.ErrorIs(t, err, errNotDirectory)
}

// The first-upload case: the root is there, nothing has been stored in it yet.
func TestListEmptyRootWithoutPrefix(t *testing.T) {
	s, err := New(Config{Path: t.TempDir()})
	require.NoError(t, err)

	objects, err := s.List(t.Context(), storage.ManifestsPrefix())
	require.NoError(t, err)
	require.Empty(t, objects)
}

// Mid-first-upload: archives are stored before the manifest, so data/ exists
// while manifests/ does not. That is an empty listing, not a broken storage -
// gc's "no manifest stored yet" protection depends on it.
func TestListMissingPrefixDirIsEmpty(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()

	s, err := New(Config{Path: root})
	require.NoError(t, err)

	archive := []byte("archive")
	require.NoError(t, s.Put(ctx, storage.ArchiveKey("first", "rs1"),
		bytes.NewReader(archive), int64(len(archive))))
	require.NoDirExists(t, filepath.Join(root, "manifests"))

	objects, err := s.List(ctx, storage.ManifestsPrefix())
	require.NoError(t, err)
	require.Empty(t, objects)
}

// The root stays lazily created: requiring it on the read path must not make a
// first backup into a fresh directory fail.
func TestPutCreatesMissingRoot(t *testing.T) {
	ctx := t.Context()
	root := filepath.Join(t.TempDir(), "fresh")

	s, err := New(Config{Path: root})
	require.NoError(t, err)

	key := storage.ManifestKey("fresh")
	data := []byte(`{"ok":true}`)
	require.NoError(t, s.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	objects, err := s.List(ctx, storage.ManifestsPrefix())
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, key, objects[0].Key)
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

func TestStaleTempFileSurvivesReadOnlyUse(t *testing.T) {
	// A leftover temp file is the residue of an interrupted upload - evidence an
	// operator may want to look at, and something tt backup verify promises never
	// to remove whatever it finds. Opening and reading the storage must therefore
	// delete nothing; only a writer sweeps.
	root := t.TempDir()
	dir := filepath.Join(root, "cluster", "production", "data")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	stale := filepath.Join(dir, tempFilePrefix+"interrupted")
	require.NoError(t, os.WriteFile(stale, []byte("half an upload"), 0o600))
	aged := time.Now().Add(-2 * staleTempFileAge)
	require.NoError(t, os.Chtimes(stale, aged, aged))

	s := newTestStorage(t, root)
	require.FileExists(t, stale, "opening the storage must not delete anything")

	_, err := s.List(t.Context(), storage.DataPrefix())
	require.NoError(t, err)
	require.FileExists(t, stale, "listing must not delete anything")

	data := []byte("archive")
	require.NoError(t, s.Put(
		t.Context(),
		storage.ArchiveKey("sweep", "11111111-1111-1111-1111-111111111111"),
		bytes.NewReader(data),
		int64(len(data)),
	))
	require.NoFileExists(t, stale, "the first put still sweeps stale temp files")
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
