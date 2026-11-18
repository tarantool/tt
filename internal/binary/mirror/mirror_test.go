package mirror_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/mirror"
)

// installation lays a tt binary installation of program with files, by
// their slash-separated names, and returns its directory.
func installation(t *testing.T, program binary.Program, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

		mode := os.FileMode(0o644)
		if name == program.Exec() {
			mode = 0o755
		}

		require.NoError(t, os.WriteFile(path, []byte(content), mode))
	}

	return dir
}

// tarantoolFiles are the files of an installation of tarantool.
func tarantoolFiles() map[string]string {
	return map[string]string{
		"tarantool":                         "#!/bin/sh\n",
		"LICENSE":                           "license",
		"AUTHORS":                           "authors",
		"include/tarantool/module.h":        "/* module */",
		"include/tarantool/lua.h":           "/* lua */",
		"share/tarantool/emmylua/box.lua":   "---@meta",
		"share/tarantool/emmylua/fiber.lua": "---@meta",
	}
}

// members reads the names and contents of the files of a gzip-compressed tar.
func members(t *testing.T, path string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	compressed, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)

	reader := tar.NewReader(compressed)
	out := map[string]string{}

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return out
		}

		require.NoError(t, err)

		content, err := io.ReadAll(reader)
		require.NoError(t, err)

		out[header.Name] = string(content)
	}
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func TestAddRepacksAnInstallation(t *testing.T) {
	dir := t.TempDir()
	request := mirror.Request{
		Program: binary.ProgramCe, Version: "3.9.0",
		Installation: installation(t, binary.ProgramCe, tarantoolFiles()),
		OS:           "linux", Arch: "amd64",
	}

	result, err := mirror.Add(context.Background(), dir, request)
	require.NoError(t, err)
	assert.Empty(t, result.Warnings)
	assert.Equal(t, filepath.Join(dir, "binaries", "tarantool-3.9.0-linux-amd64.tar.gz"),
		result.Path)
	assert.Equal(t, "the installation "+request.Installation, result.From)

	assert.Equal(t, map[string]string{
		"tarantool":                         "#!/bin/sh\n",
		"LICENSE":                           "license",
		"AUTHORS":                           "authors",
		"include/":                          "",
		"include/tarantool/":                "",
		"include/tarantool/lua.h":           "/* lua */",
		"include/tarantool/module.h":        "/* module */",
		"share/":                            "",
		"share/tarantool/":                  "",
		"share/tarantool/emmylua/":          "",
		"share/tarantool/emmylua/box.lua":   "---@meta",
		"share/tarantool/emmylua/fiber.lua": "---@meta",
	}, members(t, result.Path))

	index, found, err := mirror.ReadIndex(dir)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, index.Binaries, 1)

	info, err := os.Stat(result.Path)
	require.NoError(t, err)
	assert.Equal(t, mirror.Entry{
		Product: "tarantool", Version: "3.9.0", OS: "linux", Arch: "amd64",
		File: "tarantool-3.9.0-linux-amd64.tar.gz", SHA256: sha256Of(t, result.Path),
		Size: info.Size(),
	}, index.Binaries[0])

	// A second download of the same build packs the same bytes and replaces
	// the entry; one of another product extends the mirror.
	again, err := mirror.Add(context.Background(), dir, request)
	require.NoError(t, err)
	assert.Equal(t, result.Entry, again.Entry)

	_, err = mirror.Add(context.Background(), dir, mirror.Request{
		Program: binary.ProgramTt, Version: "3.0.0",
		Installation: installation(t, binary.ProgramTt, map[string]string{
			"tt": "#!/bin/sh\n", "LICENSE": "license",
		}),
		OS: "linux", Arch: "amd64",
	})
	require.NoError(t, err)

	index, _, err = mirror.ReadIndex(dir)
	require.NoError(t, err)
	require.Len(t, index.Binaries, 2)
	assert.Equal(t, "tarantool", index.Binaries[0].Product)
	assert.Equal(t, "tt", index.Binaries[1].Product)
	assert.Equal(t, map[string]string{"tt": "#!/bin/sh\n", "LICENSE": "license"},
		members(t, filepath.Join(dir, "binaries", index.Binaries[1].File)))
}

func TestAddRefusesATarantoolWithoutHeaders(t *testing.T) {
	files := tarantoolFiles()
	delete(files, "include/tarantool/module.h")

	dir := t.TempDir()

	_, err := mirror.Add(context.Background(), dir, mirror.Request{
		Program: binary.ProgramCe, Version: "3.9.0",
		Installation: installation(t, binary.ProgramCe, files), OS: "linux", Arch: "amd64",
	})
	require.ErrorIs(t, err, mirror.ErrContents)
	require.ErrorContains(t, err, "no include/tarantool/module.h")

	// Nothing is left behind, not even an index.
	entries, err := os.ReadDir(filepath.Join(dir, "binaries"))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestAddWarnsOfATarantoolWithoutStubs(t *testing.T) {
	files := tarantoolFiles()
	delete(files, "share/tarantool/emmylua/box.lua")
	delete(files, "share/tarantool/emmylua/fiber.lua")

	for ver, warns := range map[string]bool{"3.9.0": true, "4.0.0": true, "3.8.1": false} {
		result, err := mirror.Add(context.Background(), t.TempDir(), mirror.Request{
			Program: binary.ProgramCe, Version: ver,
			Installation: installation(t, binary.ProgramCe, files), OS: "linux", Arch: "amd64",
		})
		require.NoError(t, err)

		if warns {
			require.Len(t, result.Warnings, 1, ver)
			assert.Contains(t, result.Warnings[0], "has no EmmyLua stubs")
		} else {
			assert.Empty(t, result.Warnings, ver)
		}
	}
}

// fakeSource is a source of prebuilt archives of the given artifacts, whose
// bytes are the archives of contents by artifact name.
type fakeSource struct {
	product   string
	artifacts []bundle.Artifact
	contents  map[string][]byte
	queries   []bundle.Query
}

func (*fakeSource) Name() string { return "fake" }

func (s *fakeSource) Products() []string { return []string{s.product} }

func (s *fakeSource) List(_ context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	s.queries = append(s.queries, query)

	return s.artifacts, nil
}

func (s *fakeSource) Fetch(_ context.Context, artifact bundle.Artifact, dst io.Writer) error {
	_, err := dst.Write(s.contents[artifact.Name])

	return err
}

// tarGz packs files, by name, into a gzip-compressed tar.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var out bytes.Buffer

	compressed := gzip.NewWriter(&out)
	archive := tar.NewWriter(compressed)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		require.NoError(t, archive.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: 0o755, Size: int64(len(files[name])),
		}))

		_, err := archive.Write([]byte(files[name]))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())

	return out.Bytes()
}

func TestAddFetchesFromTheSource(t *testing.T) {
	archive := tarGz(t, map[string]string{"tt": "tt", "LICENSE": "license"})
	sum := sha256.Sum256(archive)

	source := &fakeSource{
		product: "tt",
		artifacts: []bundle.Artifact{
			{
				Version: "3.0.0", Name: "tt-3.0.0-linux-amd64.tar.gz",
				SHA256: hex.EncodeToString(sum[:]),
			},
			{Version: "3.0.1", Name: "tt-3.0.1-linux-amd64.tar.gz"},
		},
		contents: map[string][]byte{"tt-3.0.0-linux-amd64.tar.gz": archive},
	}

	dir := t.TempDir()

	result, err := mirror.Add(context.Background(), dir, mirror.Request{
		Program: binary.ProgramTt, Version: "3.0.0", Source: source, OS: "linux", Arch: "amd64",
	})
	require.NoError(t, err)
	assert.Equal(t, "fake", result.From)
	assert.Equal(t, hex.EncodeToString(sum[:]), result.Entry.SHA256)
	assert.Equal(t, int64(len(archive)), result.Entry.Size)
	// The tag of a tt release is asked for as GitHub spells it.
	require.Len(t, source.queries, 1)
	assert.Equal(t, "v3.0.0", source.queries[0].Version)
	assert.Equal(t, bundle.ChannelRelease, source.queries[0].Channel)

	// The digest the source lists is checked.
	source.artifacts[0].SHA256 = hex.EncodeToString(make([]byte, sha256.Size))

	_, err = mirror.Add(context.Background(), dir, mirror.Request{
		Program: binary.ProgramTt, Version: "3.0.0", Source: source, OS: "linux", Arch: "amd64",
	})
	require.ErrorContains(t, err, "archive checksum mismatch")

	_, err = mirror.Add(context.Background(), dir, mirror.Request{
		Program: binary.ProgramTt, Version: "3.0.2", Source: source, OS: "linux", Arch: "amd64",
	})
	require.ErrorIs(t, err, mirror.ErrNoArchive)
}

func TestAddFetchesTheGc64BuildOfAnSDK(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"tarantool-enterprise/tarantool":                  "ee",
		"tarantool-enterprise/include/tarantool/module.h": "module",
	})

	source := &fakeSource{
		product: "tarantool-ee",
		artifacts: []bundle.Artifact{
			{Version: "3.4.1-0-r63", Flavor: []string{"nogc64"}, Name: "nogc64.tar.gz"},
			{Version: "3.4.1-0-r63", Flavor: []string{"gc64"}, Name: "gc64.tar.gz"},
			{Version: "3.4.1-0-r63", Flavor: []string{"debug", "gc64"}, Name: "debug.tar.gz"},
			{Version: "3.5.0-0-r70", Flavor: []string{"gc64"}, Name: "next.tar.gz"},
		},
		contents: map[string][]byte{"gc64.tar.gz": archive},
	}

	result, err := mirror.Add(context.Background(), t.TempDir(), mirror.Request{
		Program: binary.ProgramEe, Version: "3.4.1", Source: source, OS: "linux", Arch: "amd64",
	})
	require.NoError(t, err)
	assert.Equal(t, "3.4.1-0-r63", result.Entry.Version)
	assert.Equal(t, []string{"gc64"}, result.Entry.Flavor)
	assert.Equal(t, "tarantool-ee-gc64-3.4.1-0-r63-linux-amd64.tar.gz", result.Entry.File)
	// No stubs: 3.4 shipped none.
	assert.Empty(t, result.Warnings)
	assert.Equal(t, "3.4", source.queries[0].Version)
}

func TestAddWithNoSource(t *testing.T) {
	_, err := mirror.Add(context.Background(), t.TempDir(), mirror.Request{
		Program: binary.ProgramEe, Version: "3.4.1",
		// tarantool-ee is not repacked: an installation does not say its build.
		Installation: installation(t, binary.ProgramEe, tarantoolFiles()),
		OS:           "linux", Arch: "amd64",
	})
	require.ErrorIs(t, err, mirror.ErrNoArchive)
}

func TestSource(t *testing.T) {
	dir := t.TempDir()

	for _, request := range []mirror.Request{
		{Program: binary.ProgramCe, Version: "3.9.0", OS: "linux", Arch: "amd64"},
		{Program: binary.ProgramCe, Version: "3.8.1", OS: "linux", Arch: "amd64"},
		{Program: binary.ProgramCe, Version: "3.9.0", OS: "darwin", Arch: "arm64"},
	} {
		request.Installation = installation(t, binary.ProgramCe, tarantoolFiles())

		_, err := mirror.Add(context.Background(), dir, request)
		require.NoError(t, err)
	}

	source, found, err := mirror.Open(dir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "mirror "+dir, source.Name())
	assert.Equal(t, []string{"tarantool"}, source.Products())

	archive, ok := source.Find(binary.ProgramCe, "3.8.1", "linux", "amd64")
	assert.True(t, ok)
	assert.Equal(t, "tarantool-3.8.1-linux-amd64.tar.gz", archive.Name)

	_, ok = source.Find(binary.ProgramCe, "3.8.1", "darwin", "arm64")
	assert.False(t, ok)

	for requested, matches := range map[string]bool{
		"": true, "3.8.1": true, "3.8": true, "3": true, "3.7": false, "3.8.10": false,
	} {
		assert.Equal(t, matches, source.Matches(binary.ProgramCe, requested, "linux", "amd64"),
			requested)
	}

	assert.False(t, source.Matches(binary.ProgramTt, "", "linux", "amd64"))

	query := bundle.Query{Product: "tarantool", OS: "linux", Arch: "amd64",
		Channel: bundle.ChannelRelease, Version: "3.9"}

	artifacts, err := source.List(context.Background(), query)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	assert.Equal(t, "3.9.0", artifacts[0].Version)
	assert.Equal(t, filepath.Join(dir, "binaries", "tarantool-3.9.0-linux-amd64.tar.gz"),
		artifacts[0].Locator)

	query.Version = ""
	artifacts, err = source.List(context.Background(), query)
	require.NoError(t, err)
	assert.Len(t, artifacts, 2)

	query.Channel = bundle.ChannelDev
	artifacts, err = source.List(context.Background(), query)
	require.NoError(t, err)
	assert.Empty(t, artifacts)

	// The bytes are checked against the index.
	var fetched bytes.Buffer

	require.NoError(t, source.Fetch(context.Background(), artifacts0(t, source), &fetched))

	path := filepath.Join(dir, "binaries", "tarantool-3.9.0-linux-amd64.tar.gz")
	require.NoError(t, os.WriteFile(path, []byte("tampered"), 0o644))

	err = source.Fetch(context.Background(), artifacts0(t, source), io.Discard)
	require.ErrorContains(t, err, "archive checksum mismatch")
}

// artifacts0 is the archive of tarantool 3.9.0 for linux/amd64 of source.
func artifacts0(t *testing.T, source *mirror.Source) bundle.Artifact {
	t.Helper()

	artifacts, err := source.List(context.Background(), bundle.Query{
		Product: "tarantool", OS: "linux", Arch: "amd64", Channel: bundle.ChannelRelease,
		Version: "3.9.0",
	})
	require.NoError(t, err)
	require.Len(t, artifacts, 1)

	return artifacts[0]
}

func TestReadIndex(t *testing.T) {
	dir := t.TempDir()

	_, found, err := mirror.ReadIndex(dir)
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "binaries"), 0o755))

	for content, message := range map[string]string{
		`{"format": 2, "binaries": []}`:                             "format 2, this tt reads 1",
		`{"format": 1, "binaries": [{"file": "../escape.tar.gz"}]}`: "is not a file of",
		`{"format": 1, "binaries": [{"file": "a/b.tar.gz"}]}`:       "is not a file of",
		`not json`: "invalid index of binaries",
	} {
		require.NoError(t, os.WriteFile(mirror.IndexPath(dir), []byte(content), 0o644))

		_, _, err = mirror.ReadIndex(dir)
		require.ErrorIs(t, err, mirror.ErrIndex, content)
		require.ErrorContains(t, err, message, content)
	}
}
