package emmylua_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/modules/emmylua"
	"github.com/tarantool/tt/modules/emmylua/internal/analyzer"
	"github.com/tarantool/tt/sdk/fetch"
	"github.com/tarantool/tt/sdk/sdktest"
)

// userCache gives the test a user cache of its own and returns the cache of
// the module in it, where the programs outside a project go. It changes the
// environment, so the tests that call it are not parallel.
func userCache(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	cache := analyzer.DefaultCacheDir()
	require.NotEmpty(t, cache)

	return cache
}

// rockServer writes a rock server into a new directory as rocks-repo
// publishes one - a tt-rocks-index.json naming the rock of the host by a URL
// relative to it, with its sha256 - and returns it. Each program of the rock
// prints its name and its arguments.
func rockServer(t *testing.T) string {
	t.Helper()

	release, err := analyzer.LookupRelease(analyzer.DefaultVersion)
	require.NoError(t, err)

	programs := map[analyzer.Program][]byte{}
	for _, program := range analyzer.Programs {
		programs[program] = []byte("#!/bin/sh\necho " + string(program) + " \"$@\"\n")
	}

	dir := t.TempDir()

	rock, err := analyzer.BuildRock(filepath.Join(dir, "releases"), release,
		remote.HostArch(), programs)
	require.NoError(t, err)

	data, err := os.ReadFile(rock)
	require.NoError(t, err)

	index := remote.NewRocksIndex()
	require.NoError(t, index.Add(analyzer.RockName, release.RockVersion(), remote.IndexVersion{
		Artifacts: []remote.IndexArtifact{{
			Arch: remote.HostArch(), SHA256: fetch.SHA256(data), Size: int64(len(data)),
			URL: "releases/" + filepath.Base(rock),
		}},
		Rockspec: nil,
		Yanked:   false,
	}))

	file, err := os.Create(filepath.Join(dir, remote.RocksIndexFile))
	require.NoError(t, err)
	require.NoError(t, index.Write(file))
	require.NoError(t, file.Close())

	return dir
}

// TestCheckOutsideProject checks that outside every project tt emmylua check
// and fmt run the programs of the rock server's rock, fetched into the user
// cache once and said so, with the configuration in the cache and none in
// the directory; and that a project refuses the flags that choose them. It
// runs programs and changes the environment, so it is not parallel.
func TestCheckOutsideProject(t *testing.T) {
	cache := userCache(t)
	server := rockServer(t)
	dir := t.TempDir()
	config := analyzer.GlobalConfigPath(cache, dir)

	got := sdktest.New(t, sdktest.WithProject(dir)).Run(emmylua.New,
		"emmylua", "check", "--rock-server", server)
	require.NoError(t, got.Err)
	assert.Equal(t, "emmylua_check --config "+config+" "+dir+"\n", got.Stdout)
	assert.Contains(t, got.Stderr, "outside a project: fetching EmmyLua 0.30.0pre1-1 for "+
		remote.HostArch())
	assert.FileExists(t, config)
	assert.NoDirExists(t, filepath.Join(dir, ".tarantool"))

	programs, err := filepath.Glob(filepath.Join(cache, "programs", "*", "luafmt"))
	require.NoError(t, err)
	assert.Len(t, programs, 1)

	// Again, from the cache.
	got = sdktest.New(t, sdktest.WithProject(dir)).Run(emmylua.New,
		"emmylua", "fmt", "app", "--rock-server", server)
	require.NoError(t, got.Err)
	assert.Equal(t, "luafmt --check app\n", got.Stdout)
	assert.NotContains(t, got.Stderr, "fetching")

	project := testProject(t)

	got = sdktest.New(t, sdktest.WithProject(project)).Run(emmylua.New,
		"emmylua", "check", "--rock-server", server)
	require.ErrorContains(t, got.Err, "--rock-server chooses EmmyLua outside a project; "+
		"the project "+project+" runs the one its lock pins")
}

// TestConfigOutsideProject checks that outside every project tt emmylua
// config writes the configuration into the user cache, with the modules of
// .rocks/ of the directory and of --library, fetches nothing and leaves the
// directory as it is.
func TestConfigOutsideProject(t *testing.T) {
	cache := userCache(t)
	dir := t.TempDir()

	rocks := filepath.Join(dir, ".rocks", "share", "tarantool")
	require.NoError(t, os.MkdirAll(rocks, 0o755))

	library := t.TempDir()

	got := sdktest.New(t, sdktest.WithProject(dir)).Run(emmylua.New,
		"emmylua", "config", "--library", library)
	require.NoError(t, got.Err)

	config := analyzer.GlobalConfigPath(cache, dir)
	assert.Equal(t, config+"\n", got.Stdout)
	assert.Empty(t, got.Stderr, "nothing is fetched")
	assert.NoDirExists(t, filepath.Join(dir, ".tarantool"))
	assert.NoDirExists(t, filepath.Join(cache, "programs"))

	data, err := os.ReadFile(config)
	require.NoError(t, err)

	var written struct {
		Workspace struct {
			Library []string `json:"library"`
		} `json:"workspace"`
	}

	require.NoError(t, json.Unmarshal(data, &written))
	assert.Equal(t, []string{rocks, library}, written.Workspace.Library)

	got = sdktest.New(t, sdktest.WithProject(dir)).Run(emmylua.New,
		"emmylua", "config", "--library", "absent")
	require.ErrorContains(t, got.Err, "--library absent: ")
}

// TestPrepareOutsideProject checks that tt emmylua prepare outside every
// project says that the other commands need it not.
func TestPrepareOutsideProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	services := sdktest.New(t, sdktest.WithProject(dir))

	got := services.Run(emmylua.New, "emmylua", "prepare")
	require.ErrorIs(t, got.Err, analyzer.ErrNoManifest)
	require.EqualError(t, got.Err, "not a tt project: no app.manifest.toml in "+dir+
		" or any directory above it; create one with tt new, or run tt emmylua check, "+
		"doc, fmt and ls as they are: outside a project they fetch EmmyLua into the user "+
		"cache themselves")
	assert.Empty(t, services.AddedDependencies())
}
