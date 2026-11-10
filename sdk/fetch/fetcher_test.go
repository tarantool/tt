package fetch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
)

func TestSHA256(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		fetch.SHA256(nil))
}

func TestFetchCaches(t *testing.T) {
	t.Parallel()

	body := []byte("module bytes")
	sum := fetch.SHA256(body)
	url, hits := serve(t, reply{body: body})
	fetcher := fetch.Fetcher{Client: fetch.Client{}, CacheDir: t.TempDir()}

	for range 2 {
		got, err := fetcher.Fetch(t.Context(), url, sum)
		require.NoError(t, err)
		assert.Equal(t, body, got)
	}

	assert.Equal(t, int32(1), hits.Load(), "the second fetch must come from the cache")
	assert.FileExists(t, filepath.Join(fetcher.CacheDir, sum))

	// The cache is keyed by the content: another URL of it makes no request,
	// and the sum may be given in upper case.
	got, err := fetcher.Fetch(t.Context(), "http://127.0.0.1:1/elsewhere", strings.ToUpper(sum))
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestFetchChecksum(t *testing.T) {
	t.Parallel()

	url, hits := serve(t, reply{body: []byte("tampered")})
	fetcher := fetch.Fetcher{Client: fetch.Client{}, CacheDir: t.TempDir()}
	want := fetch.SHA256([]byte("module"))

	_, err := fetcher.Fetch(t.Context(), url, want)
	require.ErrorIs(t, err, fetch.ErrChecksum)
	require.ErrorContains(t, err, "download "+url+": checksum mismatch: sha256 "+
		fetch.SHA256([]byte("tampered"))+", want "+want)
	assert.NoFileExists(t, filepath.Join(fetcher.CacheDir, want))

	entries, err := os.ReadDir(fetcher.CacheDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing unchecked is cached")

	// A sum that is not a SHA-256 makes no request.
	for _, sum := range []string{"", "not hex", want[:62]} {
		_, err = fetcher.Fetch(t.Context(), url, sum)
		require.ErrorIs(t, err, fetch.ErrChecksum)
	}

	assert.Equal(t, int32(1), hits.Load())
}

func TestFetchReplacesBadCopy(t *testing.T) {
	t.Parallel()

	body := []byte("module bytes")
	sum := fetch.SHA256(body)
	url, hits := serve(t, reply{body: body})
	fetcher := fetch.Fetcher{Client: fetch.Client{}, CacheDir: t.TempDir()}

	cached := filepath.Join(fetcher.CacheDir, sum)
	require.NoError(t, os.WriteFile(cached, []byte("corrupt"), 0o644))

	got, err := fetcher.Fetch(t.Context(), url, sum)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, int32(1), hits.Load())

	kept, err := os.ReadFile(cached)
	require.NoError(t, err)
	assert.Equal(t, body, kept, "the download replaces the bad copy")

	// A copy above the limit of the client is not read whole, nor trusted.
	require.NoError(t, os.WriteFile(cached, append(body, '!'), 0o644))

	fetcher.Client.Limit = int64(len(body))

	got, err = fetcher.Fetch(t.Context(), url, sum)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, int32(2), hits.Load())
}

func TestFetchCacheFailure(t *testing.T) {
	t.Parallel()

	body := []byte("module bytes")
	url, hits := serve(t, reply{body: body})

	// The cache directory cannot be created: a file is in its way.
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, nil, 0o644))

	for _, dir := range []string{filepath.Join(blocked, "cache"), ""} {
		fetcher := fetch.Fetcher{Client: fetch.Client{}, CacheDir: dir}

		got, err := fetcher.Fetch(t.Context(), url, fetch.SHA256(body))
		require.NoError(t, err)
		assert.Equal(t, body, got)
	}

	assert.Equal(t, int32(2), hits.Load())
}

func TestFetchErrors(t *testing.T) {
	t.Parallel()

	url, _ := serve(t, reply{status: 404})
	fetcher := fetch.Fetcher{Client: fetch.Client{}, CacheDir: t.TempDir()}

	_, err := fetcher.Fetch(t.Context(), url, fetch.SHA256(nil))
	require.ErrorIs(t, err, fetch.ErrHTTPStatus)
}
