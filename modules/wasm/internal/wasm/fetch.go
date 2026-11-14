package wasm

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/fetch"
)

// maxAssetSize bounds a downloaded asset: the native modules are tens of
// megabytes, a toolchain such as the wasi-sdk a few hundred, and a server
// that keeps sending is not one tt should trust.
const maxAssetSize = 1 << 30

// Fetcher downloads release assets, keeping each one, once its checksum is
// right, in a cache directory, so that preparing another project, or the same
// one again, does not download it again. The downloads and the cache are
// those of sdk/fetch.
type Fetcher struct {
	// CacheDir holds the assets, each named by its sha256; empty disables
	// the cache.
	CacheDir string
	// Client is the HTTP client; nil is one with fetch.DefaultTimeout.
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
	return f.FetchURL(ctx, release.AssetURL(asset), sum)
}

// FetchURL returns the content at url, checked against sum, its sha256, as
// Fetch does: the cache is keyed by sum.
func (f Fetcher) FetchURL(ctx context.Context, url, sum string) ([]byte, error) {
	fetcher := fetch.Fetcher{
		Client:   fetch.Client{HTTP: f.Client, Limit: maxAssetSize},
		CacheDir: f.CacheDir,
	}

	return fetcher.Fetch(ctx, url, sum)
}
