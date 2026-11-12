package analyzer

import (
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

// indexedServer makes dir a rock server that publishes a tt-rocks-index.json
// and nothing else of LuaRocks, as rocks-repo does: the rock of each release
// in versions for the host and for another platform, under releases/ as
// release pages hold them, named by URLs relative to the index. It returns
// the sha256 of the rock of the host of each release.
func indexedServer(t *testing.T, dir string, versions ...string) map[string]string {
	t.Helper()

	index := remote.NewRocksIndex()
	sums := map[string]string{}

	for _, version := range versions {
		release, err := LookupRelease(version)
		require.NoError(t, err)

		page := filepath.Join(dir, "releases", RockName+"-"+release.RockVersion())

		arches := []string{remote.HostArch(), "macosx-riscv64"}
		artifacts := make([]remote.IndexArtifact, 0, len(arches))

		for _, arch := range arches {
			rock, err := BuildRock(page, release, arch, testPrograms())
			require.NoError(t, err)

			data, err := os.ReadFile(rock)
			require.NoError(t, err)

			rel, err := filepath.Rel(dir, rock)
			require.NoError(t, err)

			artifacts = append(artifacts, remote.IndexArtifact{
				Arch: arch, SHA256: fetch.SHA256(data), Size: int64(len(data)),
				URL: filepath.ToSlash(rel),
			})

			if arch == remote.HostArch() {
				sums[release.RockVersion()] = fetch.SHA256(data)
			}
		}

		require.NoError(t, index.Add(RockName, release.RockVersion(), remote.IndexVersion{
			Artifacts: artifacts, Rockspec: nil, Yanked: false,
		}))
	}

	file, err := os.Create(filepath.Join(dir, remote.RocksIndexFile))
	require.NoError(t, err)
	require.NoError(t, index.Write(file))
	require.NoError(t, file.Close())

	return sums
}

// serveDir serves dir over HTTP and returns the URL of its root, ending in
// /, and the count of the requests it answered.
func serveDir(t *testing.T, dir string) (string, *atomic.Int32, *httptest.Server) {
	t.Helper()

	var requests atomic.Int32

	files := http.FileServer(http.Dir(dir))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	return server.URL + "/", &requests, server
}

// assertPrograms checks that dir holds the programs of a fake build,
// executable, each as testPrograms has it.
func assertPrograms(t *testing.T, dir string) {
	t.Helper()

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, dirPerm, info.Mode().Perm(), "the cache is readable, as the others")

	for program, want := range testPrograms() {
		path := filepath.Join(dir, string(program))

		info, err := os.Stat(path)
		require.NoError(t, err, program)
		assert.NotZero(t, info.Mode().Perm()&0o100, "%s is executable", program)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(want), string(data))
	}
}

// TestToolsFromLocalIndex checks that the programs of the newest revision
// of the default release come from a directory with a rock index into
// programs/<sha256>/ of the cache, said once, and are taken from there
// after.
func TestToolsFromLocalIndex(t *testing.T) {
	t.Parallel()

	server := t.TempDir()
	sums := indexedServer(t, server, "0.25.1", DefaultVersion)
	cache := t.TempDir()

	var progress strings.Builder

	opts := ToolsOptions{
		CacheDir: cache, RockServer: server, WorkingDir: "", Version: "", Arch: "",
		Client: fetch.Client{HTTP: nil, Limit: 0}, Progress: &progress, Log: nil,
	}

	dir, err := Tools(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "programs", sums["0.30.0pre1-1"]), dir)
	assertPrograms(t, dir)
	assert.Contains(t, progress.String(), "outside a project: fetching EmmyLua 0.30.0pre1-1 for "+
		remote.HostArch()+" (")
	assert.Contains(t, progress.String(), " MB) from "+filepath.Join(server, "releases"))

	progress.Reset()

	again, err := Tools(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, dir, again)
	assert.Empty(t, progress.String(), "nothing is fetched again")

	// Another version, by its version without the revision, relative to the
	// working directory.
	opts.Version, opts.RockServer, opts.WorkingDir = "0.25.1", filepath.Base(server),
		filepath.Dir(server)

	older, err := Tools(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "programs", sums["0.25.1-1"]), older)
}

// TestToolsOverHTTP checks that the rock comes from an HTTP server with an
// index, checked against its sha256, and that a later run takes the programs
// from the cache with no request, the server gone.
func TestToolsOverHTTP(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sums := indexedServer(t, root, DefaultVersion)
	url, requests, server := serveDir(t, root)
	cache := t.TempDir()

	opts := ToolsOptions{
		CacheDir: cache, RockServer: url, WorkingDir: "", Version: DefaultVersion, Arch: "",
		Client: fetch.Client{HTTP: nil, Limit: 0}, Progress: nil, Log: nil,
	}

	dir, err := Tools(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "programs", sums["0.30.0pre1-1"]), dir)
	assertPrograms(t, dir)
	assert.Positive(t, requests.Load())

	records, err := filepath.Glob(filepath.Join(cache, "resolved", "*.json"))
	require.NoError(t, err)
	assert.Len(t, records, 1)

	server.Close()

	offline, err := Tools(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, dir, offline, "the record and the programs serve, offline")
}

// TestToolsFromManifestServer checks that a rock server tt emmylua rock
// packs, with a manifest and no sha256, serves as well: the programs are
// named by the sha256 of the rock as read.
func TestToolsFromManifestServer(t *testing.T) {
	t.Parallel()

	build := t.TempDir()
	for program, data := range testPrograms() {
		require.NoError(t, os.WriteFile(filepath.Join(build, string(program)), data, 0o755))
	}

	server := t.TempDir()

	rocks, err := PackRocks(t.Context(), PackOptions{
		Dir: server, Version: "", From: build, Arch: "", Fetcher: fetch.Fetcher{},
		Progress: nil,
	})
	require.NoError(t, err)

	data, err := os.ReadFile(rocks[0])
	require.NoError(t, err)

	dir, err := Tools(t.Context(), ToolsOptions{
		CacheDir: t.TempDir(), RockServer: server, WorkingDir: "", Version: "", Arch: "",
		Client: fetch.Client{HTTP: nil, Limit: 0}, Progress: nil, Log: nil,
	})
	require.NoError(t, err)
	assert.Equal(t, fetch.SHA256(data), filepath.Base(dir))
	assertPrograms(t, dir)
}

// TestToolsErrors checks the failures: a rock other than the one the index
// gives the sha256 of, a version the server does not have, a server out of
// reach, which says how to work offline, and no cache.
func TestToolsErrors(t *testing.T) {
	t.Parallel()

	server := t.TempDir()
	sums := indexedServer(t, server, DefaultVersion)

	rocks, err := filepath.Glob(filepath.Join(server, "releases", "*", "*."+
		remote.HostArch()+".rock"))
	require.NoError(t, err)
	require.Len(t, rocks, 1)

	opts := ToolsOptions{
		CacheDir: t.TempDir(), RockServer: server, WorkingDir: "", Version: "9.9.9", Arch: "",
		Client: fetch.Client{HTTP: nil, Limit: 0}, Progress: nil, Log: nil,
	}

	_, err = Tools(t.Context(), opts)
	require.ErrorIs(t, err, ErrNoRock)
	require.ErrorContains(t, err, "has no emmylua 9.9.9 for "+remote.HostArch()+
		" (it has 0.30.0pre1-1)")

	require.NoError(t, os.WriteFile(rocks[0], []byte("not the rock"), 0o644))

	opts.Version = ""

	_, err = Tools(t.Context(), opts)
	require.ErrorIs(t, err, fetch.ErrChecksum)
	require.ErrorContains(t, err, sums["0.30.0pre1-1"])

	opts.RockServer = "http://127.0.0.1:1/rocks/"

	_, err = Tools(t.Context(), opts)
	require.ErrorIs(t, err, ErrRockServer)
	require.ErrorContains(t, err, "cannot reach the rock server http://127.0.0.1:1/rocks/")
	require.ErrorContains(t, err, "to work offline, run it in a project prepared with "+
		"tt emmylua prepare, or give --rock-server a directory of rocks, which "+
		"tt package download emmylua --registry "+DefaultRockServer+" --dir DIR fills "+
		"where the network is")

	opts.CacheDir = ""

	_, err = Tools(t.Context(), opts)
	require.ErrorIs(t, err, ErrNoCache)
}

// TestGlobalConfigPath checks that the configuration of a directory outside
// every project has a path of its own under the cache.
func TestGlobalConfigPath(t *testing.T) {
	t.Parallel()

	first := GlobalConfigPath("/cache", "/home/user/scripts")
	assert.Equal(t, first, GlobalConfigPath("/cache", "/home/user/scripts"))
	assert.NotEqual(t, first, GlobalConfigPath("/cache", "/home/user/other"))
	assert.Equal(t, "/cache/config", filepath.Dir(filepath.Dir(first)))
	assert.Equal(t, ConfigFileName, filepath.Base(first))
}
