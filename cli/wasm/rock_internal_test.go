package wasm

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/remote"
	"github.com/tarantool/go-luarocks/rockspec"

	"github.com/tarantool/tt/sdk/fetch"
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

	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}

	assert.ElementsMatch(t, []string{
		"wasm-0.1.0-1.rockspec", "rock_manifest", "lib/wasm.so",
	}, names)

	// The rock installs from the registry: the native backend deploys the
	// module where tarantool loads it from. A server offers a host the rocks
	// of its arch only.
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return
	}

	require.NoError(t, IndexRegistry(t.Context(), dir))

	tree := t.TempDir()
	rocks, err := client.New(luarocks.Config{
		Tree: tree, WorkingDir: t.TempDir(), Servers: []string{dir},
		Tarantool: luarocks.TarantoolConfig{Version: "3.3.0"},
	})
	require.NoError(t, err)
	require.NoError(t, rocks.Install(t.Context(), RockName, client.InstallOpts{
		Version: "0.1.0-1", Servers: nil, Deps: client.DepsNone,
	}))

	module, err := os.ReadFile(filepath.Join(tree, "lib", "tarantool", "wasm.so"))
	require.NoError(t, err)
	assert.Equal(t, "\x7fELF module", string(module))
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
	require.NoError(t, IndexRegistry(t.Context(), dir))

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

	// The registry publishes a rock index too: the version lists the rock
	// of every platform with its digest, which a lock pins.
	assert.FileExists(t, filepath.Join(dir, remote.RocksIndexFile))

	arches := make([]string, 0, len(found[0].Artifacts))
	for _, artifact := range found[0].Artifacts {
		assert.Len(t, artifact.SHA256, 64)

		arches = append(arches, artifact.Arch)
	}

	want := make([]string, 0, len(release.Platforms))
	for _, each := range release.Platforms {
		want = append(want, each.RockArch)
	}

	assert.ElementsMatch(t, want, arches)
}

// writeReleaseTable writes a release table of 0.1.0 whose two builds server
// serves, for TT_WASM_RELEASES, and returns its path.
func writeReleaseTable(t *testing.T, url string, module []byte) string {
	t.Helper()

	table := `{"0.1.0": {"version": "0.1.0", "rock_revision": 1,
		"sdk_asset": "sdk.tar.gz", "sdk_md5": "00000000000000000000000000000000",
		"base_url": "` + url + `", "platforms": [
		{"goos": "linux", "goarch": "amd64", "asset": "linux.so",
		 "sha256": "` + fetch.SHA256(module) + `",
		 "rock_arch": "linux-x86_64", "module": "wasm.so"},
		{"goos": "darwin", "goarch": "arm64", "asset": "macos.dylib",
		 "sha256": "` + fetch.SHA256(module) + `",
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

// zipNames returns the names of the files of the rock at path.
func zipNames(t *testing.T, path string) []string {
	t.Helper()

	reader, err := zip.OpenReader(path)
	require.NoError(t, err)

	t.Cleanup(func() { _ = reader.Close() })

	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}

	return names
}

// TestPackRocksFrom packs local builds of a release that has none to
// download, one platform a run, into one rock server.
func TestPackRocksFrom(t *testing.T) {
	table := `{"0.2.0": {"version": "0.2.0", "rock_revision": 1, "ref": "v0.2.0",
		"sdk_asset": "sdk.tar.gz", "sdk_md5": "00000000000000000000000000000000"}}`
	path := filepath.Join(t.TempDir(), "releases.json")
	require.NoError(t, os.WriteFile(path, []byte(table), filePerm))
	t.Setenv(ReleasesEnv, path)

	dir := t.TempDir()
	build := filepath.Join(t.TempDir(), "libtarantool_wasm_rs.so")
	require.NoError(t, os.WriteFile(build, []byte("\x7fELF module"), filePerm))

	_, err := PackRocks(t.Context(), PackOptions{
		Dir: dir, Version: "0.2.0", Fetcher: Fetcher{CacheDir: "", Client: nil},
		Progress: nil, From: "", Arch: "",
	})
	require.ErrorIs(t, err, ErrUnsupportedPlatform)
	assert.Contains(t, err.Error(), "--from")

	for _, arch := range []string{"linux-aarch64", "macosx-aarch64"} {
		rocks, err := PackRocks(t.Context(), PackOptions{
			Dir: dir, Version: "0.2.0", Fetcher: Fetcher{CacheDir: "", Client: nil},
			Progress: nil, From: build, Arch: arch,
		})
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(dir, "wasm-0.2.0-1."+arch+".rock")}, rocks)
	}

	assert.ElementsMatch(t, []string{"wasm-0.2.0-1.rockspec", "rock_manifest", "lib/wasm.so"},
		zipNames(t, filepath.Join(dir, "wasm-0.2.0-1.linux-aarch64.rock")))
	assert.ElementsMatch(t, []string{"wasm-0.2.0-1.rockspec", "rock_manifest", "lib/wasm.dylib"},
		zipNames(t, filepath.Join(dir, "wasm-0.2.0-1.macosx-aarch64.rock")))

	// The second run kept the rock of the first.
	index, err := os.ReadFile(filepath.Join(dir, registryManifestName))
	require.NoError(t, err)
	assert.Contains(t, string(index), `arch = "linux-aarch64"`)
	assert.Contains(t, string(index), `arch = "macosx-aarch64"`)

	_, err = PackRocks(t.Context(), PackOptions{
		Dir: dir, Version: "0.2.0", Fetcher: Fetcher{CacheDir: "", Client: nil},
		Progress: nil, From: build, Arch: "windows-x86_64",
	})
	require.ErrorIs(t, err, ErrUnsupportedPlatform)
}

func TestBuildRockRefusesAnotherModuleName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := BuildRock(dir, testRelease(t), Platform{
		GOOS: "linux", GOARCH: "amd64", Asset: "", SHA256: "",
		RockArch: "linux-x86_64", Module: "../wasm.so",
	}, []byte("module"))
	require.ErrorIs(t, err, ErrModuleName)
	assert.Contains(t, err.Error(), `"../wasm.so"`)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestLocalPlatformOfTheHost(t *testing.T) {
	t.Parallel()

	platform, err := LocalPlatform("")
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		require.ErrorIs(t, err, ErrUnsupportedPlatform)

		return
	}

	require.NoError(t, err)
	assert.Equal(t, hostRockArch(), platform.RockArch)
	assert.True(t, strings.HasPrefix(platform.Module, "wasm."))
}

func TestReleasesFileErrors(t *testing.T) {
	t.Setenv(ReleasesEnv, filepath.Join(t.TempDir(), "missing.json"))

	_, err := LookupRelease("0.1.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ReleasesEnv)
}
