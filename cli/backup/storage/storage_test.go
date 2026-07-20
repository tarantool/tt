package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrefixWithSlash(t *testing.T) {
	testCases := map[string]string{
		"":       "",
		"data":   "data/",
		"data/":  "data/",
		"a/b/c":  "a/b/c/",
		"a/b/c/": "a/b/c/",
	}
	for input, expected := range testCases {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, PrefixWithSlash(input))
		})
	}
}

func TestGetBytesNotFound(t *testing.T) {
	ctx := t.Context()
	s := newMemoryStorage()

	_, err := GetBytes(ctx, s, "missing")
	require.True(t, errors.Is(err, ErrKeyNotFound))
}

func TestKeyHelpers(t *testing.T) {
	require.Equal(t, "manifests/", ManifestsPrefix())
	require.Equal(t, "data/", DataPrefix())
	require.Equal(t, "manifests/20260102T030405Z.json", ManifestKey("20260102T030405Z"))
	require.Equal(t,
		"data/20260102T030405Z-550e8400-e29b-41d4-a716-446655440000.tar.zst",
		ArchiveKey("20260102T030405Z", "550e8400-e29b-41d4-a716-446655440000"),
	)
}

func TestErrKeyNotFoundComparable(t *testing.T) {
	require.True(t, errors.Is(ErrKeyNotFound, ErrKeyNotFound))
}

func TestCleanKey(t *testing.T) {
	key, err := CleanKey("/data/backup-rs1.tar.zst/")
	require.NoError(t, err)
	require.Equal(t, "data/backup-rs1.tar.zst", key)

	invalid := []string{
		"",
		"////",
		"data//backup-rs1.tar.zst",
		"data/../backup-rs1.tar.zst",
		"data/./backup-rs1.tar.zst",
		`data\backup-rs1.tar.zst`,
	}
	for _, key := range invalid {
		t.Run(key, func(t *testing.T) {
			_, err := CleanKey(key)
			require.True(t, errors.Is(err, ErrInvalidKey))
		})
	}
}

func TestCleanPrefix(t *testing.T) {
	testCases := map[string]string{
		"":             "",
		"/data/":       "data/",
		"data//":       "data/",
		"/data/backup": "data/backup",
	}
	for prefix, expected := range testCases {
		t.Run(prefix, func(t *testing.T) {
			actual, err := CleanPrefix(prefix)
			require.NoError(t, err)
			require.Equal(t, expected, actual)
		})
	}

	invalid := []string{
		"data//backup",
		"data/../",
		"data/./",
		`data\`,
	}
	for _, prefix := range invalid {
		t.Run(prefix, func(t *testing.T) {
			_, err := CleanPrefix(prefix)
			require.True(t, errors.Is(err, ErrInvalidKey))
		})
	}
}

func TestPutBytes(t *testing.T) {
	ctx := t.Context()
	s := newMemoryStorage()

	require.NoError(t, PutBytes(ctx, s, "key", []byte("value")))
	require.Equal(t, []byte("value"), s.objects["key"])
}

func TestGetBytes(t *testing.T) {
	ctx := t.Context()
	s := newMemoryStorage()
	s.objects["key"] = []byte("value")

	data, err := GetBytes(ctx, s, "key")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), data)
}

type memoryStorage struct {
	objects map[string][]byte
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{objects: make(map[string][]byte)}
}

func (s *memoryStorage) List(context.Context, string) ([]ObjectInfo, error) {
	return nil, nil
}

func (s *memoryStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memoryStorage) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read object %q: %w", key, err)
	}
	s.objects[key] = data
	return nil
}

func (s *memoryStorage) Delete(context.Context, string) error {
	return nil
}
