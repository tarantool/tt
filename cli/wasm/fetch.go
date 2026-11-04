package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// maxAssetSize bounds a downloaded asset: the native modules are tens of
	// megabytes, a toolchain such as the wasi-sdk a few hundred, and a server
	// that keeps sending is not one tt should trust.
	maxAssetSize = 1 << 30
	// downloadTimeout bounds one download.
	downloadTimeout = 10 * time.Minute
)

// Fetcher downloads release assets, keeping each one, once its checksum is
// right, in a cache directory, so that preparing another project, or the same
// one again, does not download it again.
type Fetcher struct {
	// CacheDir holds the assets, by release version; empty disables the cache.
	CacheDir string
	// Client is the HTTP client; nil is http.DefaultClient.
	Client *http.Client
}

// DefaultCacheDir is where assets are cached: <user cache>/tt/wasm, or
// nowhere when the system names no cache directory.
func DefaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, "tt", "wasm")
}

// Fetch returns the content of the release asset named asset, checked against
// sum, the sha256 tt holds the release to. A cached copy is used only when its
// checksum is right; a download is checked before it is cached or returned.
func (f Fetcher) Fetch(ctx context.Context, release Release, asset, sum string) ([]byte, error) {
	return f.FetchURL(ctx, release.AssetURL(asset), filepath.Join(release.Version, asset), sum)
}

// FetchURL returns the content at url, checked against sum, its sha256, and
// cached under the cache-relative path key.
func (f Fetcher) FetchURL(ctx context.Context, url, key, sum string) ([]byte, error) {
	cached := ""
	if f.CacheDir != "" {
		cached = filepath.Join(f.CacheDir, key)

		data, err := os.ReadFile(cached)
		if err == nil && sha256Hex(data) == sum {
			return data, nil
		}

		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("reading the cached %s: %w", filepath.Base(key), err)
		}
	}

	data, err := f.download(ctx, url)
	if err != nil {
		return nil, err
	}

	got := sha256Hex(data)
	if got != sum {
		return nil, fmt.Errorf("%w: %s has sha256 %s, want %s", ErrChecksum, url, got, sum)
	}

	if cached != "" {
		// The cache is an optimisation: failing to fill it fails nothing.
		if os.MkdirAll(filepath.Dir(cached), dirPerm) == nil {
			_ = writeFileAtomic(cached, data)
		}
	}

	return data, nil
}

// download GETs url, at most maxAssetSize bytes of it.
func (f Fetcher) download(ctx context.Context, url string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, response.Status) //nolint:err113
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxAssetSize+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}

	if len(data) > maxAssetSize {
		//nolint:err113 // The size names what went wrong; there is nothing to match.
		return nil, fmt.Errorf("downloading %s: more than %d bytes", url, maxAssetSize)
	}

	return data, nil
}

// sha256Hex returns the sha256 of data in hex.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}
