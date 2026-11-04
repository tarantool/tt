package wasm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assetServer serves body for any path and counts the requests.
func assetServer(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)

		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	return server, &hits
}

// redirectingClient sends every request to server, whatever its URL.
func redirectingClient(server *httptest.Server) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			clone := request.Clone(request.Context())

			clone.URL.Scheme = "http"
			clone.URL.Host = strings.TrimPrefix(server.URL, "http://")

			return http.DefaultTransport.RoundTrip(clone)
		}),
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       0,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// newFileServer serves files by path.
func newFileServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter, request *http.Request,
	) {
		data, ok := files[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)

			return
		}

		_, _ = writer.Write(data)
	}))
	t.Cleanup(server.Close)

	return server
}

func TestFetchChecksAndCaches(t *testing.T) {
	t.Parallel()

	body := []byte("module bytes")
	server, hits := assetServer(t, body)
	fetcher := Fetcher{CacheDir: t.TempDir(), Client: redirectingClient(server)}
	release := testRelease(t)

	for range 2 {
		got, err := fetcher.Fetch(context.Background(), release, "asset.so", sha256Hex(body))
		require.NoError(t, err)
		assert.Equal(t, body, got)
	}

	assert.Equal(t, int32(1), hits.Load(), "the second fetch must come from the cache")
	assert.FileExists(t, filepath.Join(fetcher.CacheDir, release.Version, "asset.so"))
}

func TestFetchRejectsWrongChecksum(t *testing.T) {
	t.Parallel()

	server, _ := assetServer(t, []byte("tampered"))
	fetcher := Fetcher{CacheDir: t.TempDir(), Client: redirectingClient(server)}
	release := testRelease(t)

	_, err := fetcher.Fetch(context.Background(), release, "asset.so", sha256Hex([]byte("module")))
	require.ErrorIs(t, err, ErrChecksum)
	assert.NoFileExists(t, filepath.Join(fetcher.CacheDir, release.Version, "asset.so"))
}

func TestFetchIgnoresCorruptCache(t *testing.T) {
	t.Parallel()

	body := []byte("module bytes")
	server, hits := assetServer(t, body)
	fetcher := Fetcher{CacheDir: t.TempDir(), Client: redirectingClient(server)}
	release := testRelease(t)

	cached := filepath.Join(fetcher.CacheDir, release.Version, "asset.so")
	require.NoError(t, os.MkdirAll(filepath.Dir(cached), dirPerm))
	require.NoError(t, os.WriteFile(cached, []byte("corrupt"), filePerm))

	got, err := fetcher.Fetch(context.Background(), release, "asset.so", sha256Hex(body))
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, int32(1), hits.Load())
}

func TestPrepareRequiresManifest(t *testing.T) {
	t.Parallel()

	_, err := Prepare(context.Background(), Options{
		ProjectDir:   t.TempDir(),
		Version:      "",
		Fetcher:      Fetcher{CacheDir: "", Client: nil},
		Tarawasm:     Tarawasm{Python: "", Source: "", Reinstall: false, Output: nil, binDir: ""},
		SkipTarawasm: true,
		Language:     "",
		RockServer:   "",
		Declare:      nil,
		PrepareEnv:   nil,
		Progress:     nil,
	})
	require.ErrorIs(t, err, ErrNoManifest)
}

func TestPrepareUnknownRelease(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, manifestFileName), nil, filePerm))

	_, err := Prepare(context.Background(), Options{
		ProjectDir:   project,
		Version:      "9.9.9",
		Fetcher:      Fetcher{CacheDir: "", Client: nil},
		Tarawasm:     Tarawasm{Python: "", Source: "", Reinstall: false, Output: nil, binDir: ""},
		SkipTarawasm: true,
		Language:     "",
		RockServer:   "",
		Declare:      nil,
		PrepareEnv:   nil,
		Progress:     nil,
	})
	require.ErrorIs(t, err, ErrUnknownRelease)
}

// TestPreparePacksAndDeclares runs Prepare with the network and the manifest
// edit faked: the module the server sends is checked, packed into the
// project's registry, and declared from there.
func TestPreparePacksAndDeclares(t *testing.T) {
	t.Parallel()

	release := testRelease(t)

	platform, err := release.HostPlatform()
	if err != nil {
		t.Skipf("the release has no build for this host: %v", err)
	}

	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, manifestFileName), nil, filePerm))

	module := []byte("module bytes")
	server, _ := assetServer(t, module)

	// The table holds the real checksum; the fake module needs its own.
	release.Platforms = []Platform{platform}
	release.Platforms[0].SHA256 = sha256Hex(module)

	var declared []string

	envWritten := false

	opts := Options{
		ProjectDir:   project,
		Version:      "",
		Fetcher:      Fetcher{CacheDir: "", Client: redirectingClient(server)},
		Tarawasm:     Tarawasm{Python: "", Source: "", Reinstall: false, Output: nil, binDir: ""},
		SkipTarawasm: true,
		Language:     "",
		RockServer:   "",
		Declare: func(_ context.Context, name, constraint, registry string) (bool, error) {
			declared = append(declared, name, constraint, registry)

			return false, nil
		},
		PrepareEnv: func() error {
			envWritten = true

			return nil
		},
		Progress: nil,
	}

	report := &Report{
		Release:           release,
		Platform:          release.Platforms[0],
		Rock:              "",
		Registry:          RegistryDir,
		Declared:          false,
		Tarawasm:          nil,
		TarawasmInstalled: false,
		Languages:         nil,
		Components:        nil,
	}

	require.NoError(t, prepareRock(context.Background(), opts, report))
	require.NoError(t, opts.PrepareEnv())

	registry := filepath.Join(project, filepath.FromSlash(RegistryDir))
	assert.Equal(t, filepath.Join(registry, release.RockFileName(platform)), report.Rock)
	assert.FileExists(t, filepath.Join(registry, registryManifestName))
	assert.Equal(t, []string{RockName, release.Version, RegistryDir}, declared)
	assert.True(t, report.Declared)
	assert.True(t, envWritten)
}

func TestRelinkReplacesLinkAndRefusesFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	link := filepath.Join(dir, "bin", "tarawasm")

	require.NoError(t, relink(link, "/old/tarawasm"))
	require.NoError(t, relink(link, "/new/tarawasm"))

	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, "/new/tarawasm", target)

	mine := filepath.Join(dir, "bin", "mine")
	require.NoError(t, os.WriteFile(mine, []byte("#!/bin/sh\n"), filePerm))
	require.ErrorIs(t, relink(mine, "/new/tarawasm"), errNotLink)
}

func TestReleasePlatforms(t *testing.T) {
	t.Parallel()

	release := testRelease(t)

	linux, err := release.platform("linux", "amd64")
	require.NoError(t, err)
	assert.Equal(t, "wasm.so", linux.Module)

	darwin, err := release.platform("darwin", "arm64")
	require.NoError(t, err)
	assert.Equal(t, "wasm.dylib", darwin.Module, "Tarantool on macOS loads ?.dylib only")

	_, err = release.platform("linux", "arm64")
	require.ErrorIs(t, err, ErrUnsupportedPlatform)

	assert.Equal(t,
		"https://github.com/tarantool/tarantool-wasm-rs/releases/download/v0.1.0/"+
			"tarantool-wasm-0.1.0-linux-x86_64.so",
		release.AssetURL(linux.Asset))
}
