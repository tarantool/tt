package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Storage describes a backup object storage backend.
type Storage interface {
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Delete(ctx context.Context, key string) error
}

// ObjectInfo describes one stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
}

var (
	// ErrKeyNotFound is returned when a requested object does not exist.
	ErrKeyNotFound = errors.New("storage: key not found")
	// ErrInvalidKey is returned when a key is not a canonical storage key.
	ErrInvalidKey = errors.New("storage: invalid key")
)

// GetBytes reads a small object into memory.
func GetBytes(ctx context.Context, s Storage, key string) ([]byte, error) {
	r, err := s.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object %q: %w", key, err)
	}
	defer r.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read object %q: %w", key, err)
	}

	return data, nil
}

// PutBytes stores a small object from memory.
func PutBytes(ctx context.Context, s Storage, key string, b []byte) error {
	if err := s.Put(ctx, key, bytes.NewReader(b), int64(len(b))); err != nil {
		return fmt.Errorf("failed to put object %q: %w", key, err)
	}

	return nil
}

// CleanKey returns a canonical storage object key.
func CleanKey(key string) (string, error) {
	key = strings.Trim(key, "/")
	if key == "" {
		return "", ErrInvalidKey
	}

	if err := validatePathParts(key); err != nil {
		return "", fmt.Errorf("failed to validate key path parts: %w", err)
	}

	return key, nil
}

// CleanPrefix returns a canonical storage list prefix.
func CleanPrefix(prefix string) (string, error) {
	prefix = strings.TrimLeft(prefix, "/")
	if prefix == "" {
		return "", nil
	}

	hasTrailingSlash := strings.HasSuffix(prefix, "/")
	prefix = strings.TrimRight(prefix, "/")

	if err := validatePathParts(prefix); err != nil {
		return "", fmt.Errorf("failed to validate prefix path parts: %w", err)
	}

	if hasTrailingSlash {
		return prefix + "/", nil
	}

	return prefix, nil
}

// PrefixWithSlash ensures the prefix ends with a trailing slash, unless it is empty.
func PrefixWithSlash(prefix string) string {
	if prefix == "" || strings.HasSuffix(prefix, "/") {
		return prefix
	}

	return prefix + "/"
}

func validatePathParts(key string) error {
	if key == "" || strings.Contains(key, "\\") {
		return ErrInvalidKey
	}

	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return ErrInvalidKey
		}
	}

	return nil
}
