package wasm

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/remote"
	"github.com/tarantool/go-luarocks/rockspec"
)

// testRelease is the release table entry the tests build rocks of.
func testRelease(t *testing.T) Release {
	t.Helper()

	release, err := LookupRelease(DefaultVersion)
	require.NoError(t, err)

	return release
}

func TestBuildRockLayout(t *testing.T) {
	t.Parallel()

	release := testRelease(t)
	platform, err := release.platform("linux", "amd64")
	require.NoError(t, err)

	dir := t.TempDir()

	path, err := BuildRock(dir, release, platform, []byte("\x7fELF module"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "wasm-0.1.0-1.linux-x86_64.rock"), path)

	reader, err := zip.OpenReader(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = reader.Close() })

	modes := map[string]os.FileMode{}
	for _, file := range reader.File {
		modes[file.Name] = file.Mode().Perm()
	}

	assert.Equal(t, map[string]os.FileMode{
		"wasm-0.1.0-1.rockspec": filePerm,
		"rock_manifest":         filePerm,
		"lib/wasm.so":           modulePerm,
	}, modes)
}

func TestBuildRockIsReproducible(t *testing.T) {
	t.Parallel()

	release := testRelease(t)
	platform, err := release.platform("darwin", "arm64")
	require.NoError(t, err)

	first, err := BuildRock(t.TempDir(), release, platform, []byte("module"))
	require.NoError(t, err)

	second, err := BuildRock(t.TempDir(), release, platform, []byte("module"))
	require.NoError(t, err)

	firstData, err := os.ReadFile(first)
	require.NoError(t, err)

	secondData, err := os.ReadFile(second)
	require.NoError(t, err)

	assert.Equal(t, firstData, secondData)
	assert.True(t, strings.HasSuffix(first, "wasm-0.1.0-1.macosx-aarch64.rock"))
}

// TestRockspecEvaluates checks that go-luarocks reads the rockspec tt writes,
// and reads the same source checksum from it on every platform: the lock
// records it, and one lock serves them all.
func TestRockspecEvaluates(t *testing.T) {
	t.Parallel()

	release := testRelease(t)
	path := filepath.Join(t.TempDir(), "wasm-0.1.0-1.rockspec")
	require.NoError(t, os.WriteFile(path, []byte(release.rockspec()), filePerm))

	spec, err := rockspec.Eval(path, luarocks.RockspecConfig{})
	require.NoError(t, err)

	assert.Equal(t, RockName, spec.Package)
	assert.Equal(t, "0.1.0-1", spec.Version)
	assert.Equal(t, release.SDKMD5, spec.Source.MD5)
	assert.Equal(t, release.AssetURL(release.SDKAsset), spec.Source.URL)
}

// TestIndexedRegistryServesHostRock checks the registry through the index
// go-luarocks resolves against: the rock of the host's arch is offered, under
// the version the lock pins.
func TestIndexedRegistryServesHostRock(t *testing.T) {
	t.Parallel()

	release := testRelease(t)

	platform, err := release.HostPlatform()
	if err != nil {
		t.Skipf("the release has no build for this host: %v", err)
	}

	dir := t.TempDir()

	for _, each := range release.Platforms {
		_, err = BuildRock(dir, release, each, []byte("module"))
		require.NoError(t, err)
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("not a rock"), filePerm))
	require.NoError(t, IndexRegistry(dir))

	index := remote.NewIndex(dir, remote.IndexOptions{
		InsecureServers: nil,
		UserAgent:       "",
		LuaVersion:      "",
		Arch:            "",
		Cache:           nil,
	})

	found, err := index.Query(context.Background(), RockName, "")
	require.NoError(t, err)
	require.Len(t, found, 1)

	assert.Equal(t, "0.1.0-1", found[0].Version.Raw)
	assert.True(t, strings.HasSuffix(found[0].URL, release.RockFileName(platform)),
		"got %s", found[0].URL)
}

// writeReleaseTable writes a release table of 0.1.0 whose two builds server
// serves, for TT_WASM_RELEASES, and returns its path.
func writeReleaseTable(t *testing.T, url string, module []byte) string {
	t.Helper()

	table := `{"0.1.0": {"version": "0.1.0", "rock_revision": 1,
		"sdk_asset": "sdk.tar.gz", "sdk_md5": "00000000000000000000000000000000",
		"base_url": "` + url + `", "platforms": [
		{"goos": "linux", "goarch": "amd64", "asset": "linux.so",
		 "sha256": "` + sha256Hex(module) + `",
		 "rock_arch": "linux-x86_64", "module": "wasm.so"},
		{"goos": "darwin", "goarch": "arm64", "asset": "macos.dylib",
		 "sha256": "` + sha256Hex(module) + `",
		 "rock_arch": "macosx-aarch64", "module": "wasm.dylib"}
	]}}`
	path := filepath.Join(t.TempDir(), "releases.json")
	require.NoError(t, os.WriteFile(path, []byte(table), filePerm))

	return path
}

// TestPackRocks packs the rocks of a release of its own: the table of
// TT_WASM_RELEASES, whose assets a fake server serves under its base URL.
func TestPackRocks(t *testing.T) {
	module := []byte("module bytes")
	server := newFileServer(t, map[string][]byte{
		"/v0.1.0/linux.so": module, "/v0.1.0/macos.dylib": module,
	})
	t.Setenv(ReleasesEnv, writeReleaseTable(t, server.URL, module))

	assert.Equal(t, []string{"0.1.0"}, Versions())

	release, err := LookupRelease("0.1.0")
	require.NoError(t, err)
	assert.Equal(t, server.URL+"/v0.1.0/linux.so", release.AssetURL("linux.so"))

	dir := t.TempDir()

	rocks, err := PackRocks(context.Background(), PackOptions{
		Dir: dir, Version: "", Fetcher: Fetcher{CacheDir: "", Client: nil}, Progress: nil,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "wasm-0.1.0-1.linux-x86_64.rock"),
		filepath.Join(dir, "wasm-0.1.0-1.macosx-aarch64.rock"),
	}, rocks)

	index, err := os.ReadFile(filepath.Join(dir, registryManifestName))
	require.NoError(t, err)
	assert.Contains(t, string(index), `arch = "linux-x86_64"`)
	assert.Contains(t, string(index), `arch = "macosx-aarch64"`)
}

func TestReleasesFileErrors(t *testing.T) {
	t.Setenv(ReleasesEnv, filepath.Join(t.TempDir(), "missing.json"))

	_, err := LookupRelease("0.1.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ReleasesEnv)
}
