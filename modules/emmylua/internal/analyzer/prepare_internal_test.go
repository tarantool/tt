package analyzer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
)

// testAsset packs data as name at the top of a tar.gz, as the assets of
// EmmyLua hold their program.
func testAsset(t *testing.T, name string, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	compressed := gzip.NewWriter(&buf)
	archive := tar.NewWriter(compressed)

	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: "./", Mode: 0o755,
	}))
	require.NoError(t, archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "./" + name, Mode: 0o755, Size: int64(len(data)),
	}))

	_, err := archive.Write(data)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())

	return buf.Bytes()
}

// serveAssets serves assets by their paths and returns the URL of the
// server and the count of the requests it answered.
func serveAssets(t *testing.T, assets map[string][]byte) (string, *atomic.Int32) {
	t.Helper()

	var requests atomic.Int32

	serve := func(writer http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		asset, ok := assets[r.URL.Path]
		if !ok {
			http.NotFound(writer, r)

			return
		}

		_, _ = writer.Write(asset)
	}

	server := httptest.NewServer(http.HandlerFunc(serve))
	t.Cleanup(server.Close)

	return server.URL, &requests
}

// hostAssets returns the platform of the host whose asset of each program
// holds what content returns for it, and the assets by their paths.
func hostAssets(t *testing.T, content func(Program) (string, []byte)) (
	Platform, map[string][]byte,
) {
	t.Helper()

	assets := map[string][]byte{}
	platform := Platform{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Suffix: "test",
		SHA256: map[Program]string{},
	}

	for _, program := range Programs {
		name, data := content(program)
		asset := testAsset(t, name, data)

		assets["/"+platform.Asset(program)] = asset
		platform.SHA256[program] = fetch.SHA256(asset)
	}

	return platform, assets
}

// assetRelease serves the assets of a release for the host and writes the
// releases file that names them, returning its path and the request count.
func assetRelease(t *testing.T) (string, *atomic.Int32) {
	t.Helper()

	platform, assets := hostAssets(t, func(program Program) (string, []byte) {
		return string(program), testPrograms()[program]
	})
	url, requests := serveAssets(t, assets)

	data, err := json.Marshal(map[string]Release{DefaultVersion: {
		Version: DefaultVersion, RockRevision: 1, Ref: "abc", SourceMD5: "00",
		Platforms: []Platform{platform}, BaseURL: url,
	}})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "releases.json")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	return path, requests
}

// testProject is a directory with a manifest.
func testProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName),
		[]byte("manifest_version = \"0.1\"\n"), 0o644))

	return dir
}

// declared records the calls of Declare.
type declared struct {
	calls []string
}

func (d *declared) declare(_ context.Context, name, constraint, registry string) (bool, error) {
	d.calls = append(d.calls, name+" "+constraint+" "+registry)

	return len(d.calls) > 1, nil
}

func TestPrepareFrom(t *testing.T) {
	t.Parallel()

	build := t.TempDir()
	for program, data := range testPrograms() {
		require.NoError(t, os.WriteFile(filepath.Join(build, string(program)), data, 0o755))
	}

	project := testProject(t)
	decl := &declared{}
	installs := 0

	var progress strings.Builder

	opts := Options{
		ProjectDir: project, From: build, Declare: decl.declare,
		// What follows the declaration comes under its line, before the
		// install.
		Declared: func() { progress.WriteString("  (moves)\n") },
		Install:  func(context.Context) error { installs++; return nil },
		Progress: &progress,
	}

	report, err := Prepare(t.Context(), opts)
	require.NoError(t, err)
	assert.Contains(t, progress.String(),
		"in [dev_dependencies]\n  (moves)\ninstalling the rocks\n")
	assert.True(t, report.Declared)
	assert.True(t, report.Installed)
	assert.Equal(t, RegistryDir, report.Registry)
	assert.Equal(t, filepath.Join(project, ".tarantool", "registry",
		"emmylua-0.30.0pre1-1."+RockArch(runtime.GOOS, runtime.GOARCH)+".rock"), report.Rock)
	assert.FileExists(t, filepath.Join(project, ".tarantool", "registry", "manifest"))
	assert.Equal(t, []string{"emmylua 0.30.0pre1 .tarantool/registry"}, decl.calls)
	assert.Equal(t, 1, installs)

	first, err := os.ReadFile(report.Rock)
	require.NoError(t, err)

	// Again: the same rock, the declaration kept.
	report, err = Prepare(t.Context(), opts)
	require.NoError(t, err)
	assert.False(t, report.Declared)
	assert.Contains(t, progress.String(), "already declared")

	second, err := os.ReadFile(report.Rock)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

// TestPrepareRockServer checks that the rock is declared from the rock server
// without anything packed, from DefaultRockServer when none is named, and at
// any version: the server, not the table of releases, knows which it serves.
func TestPrepareRockServer(t *testing.T) {
	t.Parallel()

	project := testProject(t)
	decl := &declared{}

	report, err := Prepare(t.Context(), Options{
		ProjectDir: project, Version: "0.25.1", RockServer: "https://rocks.example/",
		Declare: decl.declare,
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rock)
	assert.False(t, report.Installed)
	assert.Equal(t, "https://rocks.example/", report.Registry)
	assert.Equal(t, []string{"emmylua 0.25.1 https://rocks.example/"}, decl.calls)

	decl = &declared{}

	report, err = Prepare(t.Context(), Options{ProjectDir: project, Declare: decl.declare})
	require.NoError(t, err)
	assert.Equal(t, DefaultRockServer, report.Registry)
	assert.Equal(t, Release{Version: DefaultVersion}, report.Release)
	assert.Equal(t, []string{"emmylua 0.30.0pre1 " + DefaultRockServer}, decl.calls)

	decl = &declared{}

	_, err = Prepare(t.Context(), Options{
		ProjectDir: project, Version: "9.9.9", Declare: decl.declare,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"emmylua 9.9.9 " + DefaultRockServer}, decl.calls)

	assert.NoDirExists(t, filepath.Join(project, ".tarantool"))
}

func TestPrepareErrors(t *testing.T) {
	t.Parallel()

	_, err := Prepare(t.Context(), Options{ProjectDir: t.TempDir()})
	require.ErrorIs(t, err, ErrNoManifest)

	_, err = Prepare(t.Context(), Options{ProjectDir: testProject(t), From: t.TempDir()})
	require.ErrorIs(t, err, ErrBuild)

	// A local build is packed with the rockspec of a release tt knows.
	_, err = Prepare(t.Context(), Options{
		ProjectDir: testProject(t), Version: "9.9.9", From: t.TempDir(),
	})
	require.ErrorIs(t, err, ErrUnknownRelease)
}

//nolint:paralleltest // Sets the environment.
func TestPackRocksDownloads(t *testing.T) {
	releasesFile, requests := assetRelease(t)
	t.Setenv(ReleasesEnv, releasesFile)

	fetcher := fetch.Fetcher{CacheDir: t.TempDir()}

	rocks, err := PackRocks(t.Context(), PackOptions{Dir: t.TempDir(), Fetcher: fetcher})
	require.NoError(t, err)
	require.Len(t, rocks, 1)
	assert.FileExists(t, rocks[0])
	assert.EqualValues(t, len(Programs), requests.Load())

	// The cache serves the second.
	_, err = PackRocks(t.Context(), PackOptions{Dir: t.TempDir(), Fetcher: fetcher})
	require.NoError(t, err)
	assert.EqualValues(t, len(Programs), requests.Load())

	// A checksum that differs fails.
	release, err := LookupRelease(DefaultVersion)
	require.NoError(t, err)

	release.Platforms[0].SHA256[Check] = strings.Repeat("0", 64)

	_, err = fetchPrograms(t.Context(), fetch.Fetcher{}, release, release.Platforms[0])
	require.ErrorIs(t, err, fetch.ErrChecksum)
}

// TestFetchProgramsNotInAsset checks that an asset that does not hold its
// program fails, its checksum right as it is.
func TestFetchProgramsNotInAsset(t *testing.T) {
	t.Parallel()

	platform, assets := hostAssets(t, func(Program) (string, []byte) {
		return "other", []byte("x")
	})
	url, _ := serveAssets(t, assets)

	release := Release{
		Version: "1.0.0", RockRevision: 1, Ref: "abc", SourceMD5: "00",
		Platforms: []Platform{platform}, BaseURL: url,
	}

	_, err := fetchPrograms(t.Context(), fetch.Fetcher{}, release, platform)
	require.ErrorIs(t, err, fetch.ErrNotInArchive)
	assert.Contains(t, err.Error(), platform.Asset(Check))
}

// TestPackRocksWithoutBuilds checks that a release with no builds to
// download fails, naming what packs or takes its rocks instead.
func TestPackRocksWithoutBuilds(t *testing.T) {
	t.Parallel()

	_, err := PackRocks(t.Context(), PackOptions{Dir: t.TempDir(), Version: DefaultVersion})
	require.ErrorIs(t, err, ErrUnsupportedPlatform)
	require.ErrorContains(t, err, "EmmyLua 0.30.0pre1 has no builds to download: pack it "+
		"from a build of 599c33862491c5ca0f325e0ef549ad34c510ca97 (--from), or pack 0.25.1, "+
		"which has (--version 0.25.1); tt package download emmylua@0.30.0pre1 --registry "+
		DefaultRockServer+" --dir DIR takes the rock of this host the rock index serves")
}

//nolint:paralleltest // Sets the environment.
func TestPackRocks(t *testing.T) {
	releasesFile, _ := assetRelease(t)
	t.Setenv(ReleasesEnv, releasesFile)

	fetcher := fetch.Fetcher{}
	out := t.TempDir()

	rocks, err := PackRocks(t.Context(), PackOptions{Dir: out, Fetcher: fetcher})
	require.NoError(t, err)
	require.Len(t, rocks, 1)
	assert.FileExists(t, filepath.Join(out, "manifest"))

	// A local build joins them as the rock of its arch.
	build := t.TempDir()
	for program, data := range testPrograms() {
		require.NoError(t, os.WriteFile(filepath.Join(build, string(program)), data, 0o755))
	}

	rocks, err = PackRocks(t.Context(), PackOptions{
		Dir: out, From: build, Arch: "macosx-riscv64", Fetcher: fetcher,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(out, "emmylua-0.30.0pre1-1.macosx-riscv64.rock")}, rocks)

	entries, err := filepath.Glob(filepath.Join(out, "*.rock"))
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}
