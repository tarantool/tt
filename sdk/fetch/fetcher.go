package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// cacheDirPerm and cacheFilePerm are the modes the cache is written with.
	cacheDirPerm  os.FileMode = 0o755
	cacheFilePerm os.FileMode = 0o644
)

// Fetcher downloads files of a known SHA-256 and keeps the checked copies in
// a cache directory, so that the next fetch of the same file, from the same
// URL or another one, makes no request.
type Fetcher struct {
	// Client makes the requests; its limit bounds a cached copy as well.
	Client Client
	// CacheDir holds the checked copies, each named by its SHA-256 in hex;
	// empty disables the cache.
	CacheDir string
}

// Fetch returns the body at url, checked against sum, its SHA-256 in hex. A
// cached copy is used, with no request, only when its own SHA-256 is sum; a
// download is checked before it is cached or returned, and one with another
// SHA-256 wraps ErrChecksum, as does a sum that is not a SHA-256 in hex. The
// cache is an optimisation: failing to read or to fill it fails nothing.
func (f Fetcher) Fetch(ctx context.Context, url, sum string) ([]byte, error) {
	want, ok := parseSHA256(sum)
	if !ok {
		return nil, fmt.Errorf("download %s: %w: %q is not a SHA-256 in hex",
			url, ErrChecksum, sum)
	}

	cached := ""
	if f.CacheDir != "" {
		cached = filepath.Join(f.CacheDir, want)

		data, ok := readCached(cached, want, f.Client.limit())
		if ok {
			return data, nil
		}
	}

	data, err := f.Client.Bytes(ctx, url)
	if err != nil {
		return nil, err
	}

	got := SHA256(data)
	if got != want {
		return nil, fmt.Errorf("download %s: %w: sha256 %s, want %s", url, ErrChecksum, got, want)
	}

	if cached != "" {
		_ = writeCached(cached, data)
	}

	return data, nil
}

// SHA256 returns the SHA-256 of data in lowercase hex, the form Fetch checks.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// parseSHA256 returns sum in lowercase hex and whether it is a SHA-256.
func parseSHA256(sum string) (string, bool) {
	digest, err := hex.DecodeString(sum)
	if err != nil || len(digest) != sha256.Size {
		return "", false
	}

	return hex.EncodeToString(digest), true
}

// readCached returns the cached copy at path and whether it is one: a copy
// that is missing, unreadable, above limit or of a SHA-256 other than sum is
// none.
func readCached(path, sum string, limit int64) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || SHA256(data) != sum {
		return nil, false
	}

	return data, true
}

// writeCached keeps data at path through a temporary file in the same
// directory and a rename, so that a reader never sees a part of it.
func writeCached(path string, data []byte) error {
	dir := filepath.Dir(path)

	err := os.MkdirAll(dir, cacheDirPerm)
	if err != nil {
		return fmt.Errorf("create the cache %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(cacheFilePerm)
	}

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
