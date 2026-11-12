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

	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/sdk/fetch"
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
		got, err := fetcher.Fetch(context.Background(), release, "asset.so", fetch.SHA256(body))
		require.NoError(t, err)
		assert.Equal(t, body, got)
	}

	assert.Equal(t, int32(1), hits.Load(), "the second fetch must come from the cache")
	assert.FileExists(t, filepath.Join(fetcher.CacheDir, fetch.SHA256(body)))
}

func TestFetchRejectsWrongChecksum(t *testing.T) {
	t.Parallel()

	server, _ := assetServer(t, []byte("tampered"))
	fetcher := Fetcher{CacheDir: t.TempDir(), Client: redirectingClient(server)}
	release := testRelease(t)

	want := fetch.SHA256([]byte("module"))

	_, err := fetcher.Fetch(context.Background(), release, "asset.so", want)
	require.ErrorIs(t, err, ErrChecksum)
	assert.NoFileExists(t, filepath.Join(fetcher.CacheDir, want))
}

func TestFetchIgnoresCorruptCache(t *testing.T) {
	t.Parallel()

	body := []byte("module bytes")
	server, hits := assetServer(t, body)
	fetcher := Fetcher{CacheDir: t.TempDir(), Client: redirectingClient(server)}
	release := testRelease(t)

	cached := filepath.Join(fetcher.CacheDir, fetch.SHA256(body))
	require.NoError(t, os.WriteFile(cached, []byte("corrupt"), filePerm))

	got, err := fetcher.Fetch(context.Background(), release, "asset.so", fetch.SHA256(body))
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, int32(1), hits.Load())
}

// prepareOptions are the options of a Prepare of project that installs no
// tarawasm and records the declarations it makes in declared.
func prepareOptions(project string, declared *[]string) Options {
	return Options{
		ProjectDir:   project,
		Version:      "",
		Fetcher:      Fetcher{CacheDir: "", Client: nil},
		Tarawasm:     Tarawasm{Python: "", Source: "", Reinstall: false, Output: nil, binDir: ""},
		SkipTarawasm: true,
		Language:     "",
		From:         "",
		RockServer:   "",
		Declare: func(_ context.Context, name, constraint, registry string) (bool, error) {
			*declared = append(*declared, name+" "+constraint+" "+registry)

			return false, nil
		},
		PrepareEnv: nil,
		Progress:   nil,
	}
}

// testProject is a directory with a manifest.
func testProject(t *testing.T) string {
	t.Helper()

	project := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(project, manifestFileName), nil, filePerm))

	return project
}

func TestPrepareRequiresManifest(t *testing.T) {
	t.Parallel()

	var declared []string

	_, err := Prepare(context.Background(), prepareOptions(t.TempDir(), &declared))
	require.ErrorIs(t, err, ErrNoManifest)
	assert.Empty(t, declared)
}

// TestPrepareFromUnknownRelease checks that a local build is packed only for
// a release tt knows: the rockspec is written from it.
func TestPrepareFromUnknownRelease(t *testing.T) {
	t.Parallel()

	var declared []string

	opts := prepareOptions(testProject(t), &declared)

	opts.Version = "9.9.9"
	opts.From = filepath.Join(t.TempDir(), "module.so")

	_, err := Prepare(context.Background(), opts)
	require.ErrorIs(t, err, ErrUnknownRelease)
	assert.Empty(t, declared)
}

// TestPrepareDeclaresFromTheRockServer checks that, without a local build,
// nothing is packed or downloaded and the rock is declared from the rock
// server: DefaultRockServer when none is named, and at any version.
func TestPrepareDeclaresFromTheRockServer(t *testing.T) {
	t.Parallel()

	project := testProject(t)

	var declared []string

	report, err := Prepare(context.Background(), prepareOptions(project, &declared))
	require.NoError(t, err)
	assert.Equal(t, DefaultRockServer, report.Registry)
	assert.Empty(t, report.Rock)
	assert.Equal(t, hostRockArch(), report.Platform.RockArch)
	assert.True(t, report.Declared)

	opts := prepareOptions(project, &declared)

	opts.Version = "9.9.9"
	opts.RockServer = "https://rocks.example/"

	report, err = Prepare(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "https://rocks.example/", report.Registry)
	assert.Equal(t, "9.9.9", report.Release.Version)

	assert.Equal(t, []string{
		"wasm 0.1.0 " + DefaultRockServer,
		"wasm 9.9.9 https://rocks.example/",
	}, declared)
	assert.NoDirExists(t, filepath.Join(project, ".tarantool"))
}

// TestPreparePacksTheLocalBuild checks that --from packs the module as the
// rock of this host into the project's registry, indexes it and declares
// the rock from there.
func TestPreparePacksTheLocalBuild(t *testing.T) {
	t.Parallel()

	_, err := LocalPlatform("")
	if err != nil {
		t.Skipf("no rock for this host: %v", err)
	}

	project := testProject(t)
	module := filepath.Join(t.TempDir(), "module.so")
	require.NoError(t, os.WriteFile(module, []byte("module bytes"), filePerm))

	var declared []string

	envWritten := false

	opts := prepareOptions(project, &declared)

	opts.From = module
	opts.PrepareEnv = func() error {
		envWritten = true

		return nil
	}

	report, err := Prepare(context.Background(), opts)
	require.NoError(t, err)

	registry := filepath.Join(project, filepath.FromSlash(RegistryDir))

	assert.Equal(t, filepath.Join(registry, report.Release.RockFileName(report.Platform)),
		report.Rock)
	assert.Equal(t, "wasm-0.1.0-1."+hostRockArch()+".rock", filepath.Base(report.Rock))
	assert.FileExists(t, filepath.Join(registry, registryManifestName))
	// The registry of the project gets no rock index: the lock would pin the
	// digest of a rock packed anew from every local build.
	assert.NoFileExists(t, filepath.Join(registry, remote.RocksIndexFile))
	assert.Equal(t, []string{"wasm 0.1.0 " + RegistryDir}, declared)
	assert.Equal(t, RegistryDir, report.Registry)
	assert.True(t, report.Declared)
	assert.True(t, envWritten)

	opts.From = filepath.Join(t.TempDir(), "none.so")

	_, err = Prepare(context.Background(), opts)
	require.ErrorContains(t, err, "reading the build")
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
