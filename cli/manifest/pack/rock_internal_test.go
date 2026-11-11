package pack

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// libraryManifest is a pure Lua library: a module under its own name and a
// role under roles, as a Tarantool module lays itself out.
const libraryManifest = `manifest_version = '0.1'

[package]
name = 'mylib'
description = 'A test library'
license = 'BSD-2-Clause'
repository = 'https://github.com/example/mylib'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'
platforms = ['linux-amd64', 'darwin-arm64']

[components.mylib]
path = 'mylib'

[components.roles]
path = 'roles'
namespace = 'roles'
`

// writeLibrary writes the project of libraryManifest at version 1.0.0.
func writeLibrary(t *testing.T) string {
	t.Helper()

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName:  libraryManifest,
		versionFileName:   "1.0.0\n",
		"mylib/init.lua":  "return {}\n",
		"mylib/util.lua":  "return {}\n",
		"roles/mylib.lua": "return {}\n",
	})

	return src
}

// packOptions are the options of a pack of src into out, of formats.
func packOptions(src, out string, formats ...string) Options {
	return Options{
		ProjectDir: src,
		Formats:    formats,
		OutputDir:  out,
		Build: build.Options{
			TtVersion: "tt test",
			Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
		},
	}
}

// zipEntries reads the entries of the zip archive at path by name.
func zipEntries(t *testing.T, path string) map[string]string {
	t.Helper()

	archive, err := zip.OpenReader(path)
	require.NoError(t, err)

	defer func() { _ = archive.Close() }()

	entries := map[string]string{}

	for _, file := range archive.File {
		reader, err := file.Open()
		require.NoError(t, err)

		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())

		entries[file.Name] = string(data)
	}

	return entries
}

// sortedKeys lists the keys of entries in order.
func sortedKeys(entries map[string]string) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

// TestPackRockOfALuaLibrary: a package with no native file is an .all rock
// of its laid-out files, its rockspec beside it the one inside it, and the
// output directory is a rock server; no .tt archive is made.
func TestPackRockOfALuaLibrary(t *testing.T) {
	t.Parallel()

	src := writeLibrary(t)
	out := filepath.Join(src, "_out")

	result, err := Run(context.Background(), packOptions(src, out, FormatRock))
	require.NoError(t, err)

	assert.Empty(t, result.Path)
	assert.Equal(t, filepath.Join(out, "mylib-1.0.0-1.all.rock"), result.Rock)
	assert.Equal(t, filepath.Join(out, "mylib-1.0.0-1.rockspec"), result.Rockspec)

	entries := zipEntries(t, result.Rock)
	assert.Equal(t, []string{
		"lua/mylib/init.lua",
		"lua/mylib/util.lua",
		"lua/mylib/version.lua",
		"lua/roles/mylib.lua",
		"mylib-1.0.0-1.rockspec",
		"rock_manifest",
	}, sortedKeys(entries))

	rockspec, err := os.ReadFile(result.Rockspec)
	require.NoError(t, err)
	assert.Equal(t, entries["mylib-1.0.0-1.rockspec"], string(rockspec))
	assert.Contains(t, string(rockspec), `url = "git+https://github.com/example/mylib",`)
	assert.Contains(t, string(rockspec), `supported_platforms = { "linux", "macosx" }`)
	assert.Contains(t, string(rockspec), "type = \"none\"")
	// A VERSION file is no tag.
	assert.NotContains(t, string(rockspec), "tag =")

	assert.FileExists(t, filepath.Join(out, "manifest"))

	matches, err := filepath.Glob(filepath.Join(out, "*"+archiveExt))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

// TestPackRockInstallsIntoAProject: the rock is what another project
// depends on, from the output directory as its rock server.
func TestPackRockInstallsIntoAProject(t *testing.T) {
	t.Parallel()

	src := writeLibrary(t)
	out := filepath.Join(t.TempDir(), "server")

	_, err := Run(context.Background(), packOptions(src, out, FormatRock))
	require.NoError(t, err)

	consumer := t.TempDir()
	writeTree(t, consumer, map[string]string{
		manifestFileName: fmt.Sprintf(`manifest_version = '0.1'

[package]
name = 'consumer'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
mylib = { version = '>=1.0.0', registry = '%s' }

[components.app]
path = '.'
include = ['init.lua']
`, out),
		versionFileName: "0.1.0\n",
		"init.lua":      "return require('mylib')\n",
	})

	err = build.Run(context.Background(), build.Options{
		ProjectDir: consumer,
		TtVersion:  "tt test",
		Tarantool:  rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
	})
	require.NoError(t, err)

	share := filepath.Join(consumer, rocksDirName, "share", "tarantool")
	assert.FileExists(t, filepath.Join(share, "mylib", "init.lua"))
	assert.FileExists(t, filepath.Join(share, "mylib", "util.lua"))
	assert.FileExists(t, filepath.Join(share, "roles", "mylib.lua"))

	lock, err := os.ReadFile(filepath.Join(consumer, lockFileName))
	require.NoError(t, err)
	assert.Contains(t, string(lock), "version = '1.0.0-1'")
}

// TestPackRockAndArchiveOfAProgram: a program an executable component built
// makes the rock the binary rock of the host, packed with the .tt archive from
// one build at the revision asked; a product without the program is an .all
// rock.
func TestPackRockAndArchiveOfAProgram(t *testing.T) {
	t.Parallel()

	helper, err := os.Executable()
	require.NoError(t, err)

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName: fmt.Sprintf(executableManifest, helper),
		versionFileName:  "1.0.0\n",
		"init.lua":       "return 1\n",
		"helper/README":  "the helper",
	})

	out := filepath.Join(src, "_out")

	opts := packOptions(src, out, FormatTt, FormatRock)

	opts.WithoutDeps = true
	opts.Revision = 3

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	assert.FileExists(t, result.Path)
	assert.Equal(t, filepath.Join(out, "exec-app-1.0.0-3."+remote.HostArch()+".rock"), result.Rock)
	assert.Contains(t, zipEntries(t, result.Rock), "bin/helper")

	// Another product names no rock of its own: it is no rock to pack.
	opts = packOptions(src, out, FormatRock)
	opts.Product = "lua-only"

	_, err = Run(context.Background(), opts)
	require.ErrorIs(t, err, errNoRock)
	assert.Contains(t, err.Error(), "[products.lua-only].rock")
}

// productsManifest is a package of two products released as two rocks: the
// module under the package name and its tools under a rock of their own.
const productsManifest = `manifest_version = '0.1'

[package]
name = 'multi'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.lib]
path = 'lib'
namespace = 'multi'

[components.tools]
path = 'tools'
namespace = 'multi_tools'

[products.default]
components = ['lib']
default = true

[products.tools]
components = ['tools']
rock = 'multi-tools'
`

// TestPackRockOfAProduct: a product is packed as the rock it names, with its
// own components alone; the default one as the rock of the package.
func TestPackRockOfAProduct(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName:   productsManifest,
		versionFileName:    "2.0.0\n",
		"lib/init.lua":     "return {}\n",
		"tools/init.lua":   "return {}\n",
		"tools/report.lua": "return {}\n",
	})

	out := filepath.Join(src, "_out")

	opts := packOptions(src, out, FormatRock)

	opts.Product = "tools"

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(out, "multi-tools-2.0.0-1.all.rock"), result.Rock)
	// version.lua is the tools rock's own, so the two rocks share no file.
	assert.Equal(t, []string{
		"lua/multi-tools/version.lua",
		"lua/multi_tools/init.lua",
		"lua/multi_tools/report.lua",
		"multi-tools-2.0.0-1.rockspec",
		"rock_manifest",
	}, sortedKeys(zipEntries(t, result.Rock)))

	result, err = Run(context.Background(), packOptions(src, out, FormatRock))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(out, "multi-2.0.0-1.all.rock"), result.Rock)
	assert.Equal(t, []string{
		"lua/multi/init.lua",
		"lua/multi/version.lua",
		"multi-2.0.0-1.rockspec",
		"rock_manifest",
	}, sortedKeys(zipEntries(t, result.Rock)))
}

// TestPackRockRefusesForeignNative: the rock of the host cannot carry a native
// file of another platform.
func TestPackRockRefusesForeignNative(t *testing.T) {
	t.Parallel()

	foreign, _ := foreignNative(t)

	src := writeLibrary(t)
	writeTree(t, src, map[string]string{"mylib/fast.so": string(foreign)})

	_, err := Run(context.Background(), packOptions(src, filepath.Join(src, "_out"), FormatRock))
	require.ErrorIs(t, err, errForeignNative)
}

// TestCheckFormats: a format pack does not know, and the options of the
// archive for a pack that makes none, fail before anything is built.
func TestCheckFormats(t *testing.T) {
	t.Parallel()

	require.NoError(t, checkFormats(Options{}))
	require.NoError(t, checkFormats(Options{Formats: []string{FormatTt, FormatRock}}))
	require.NoError(t, checkFormats(Options{
		Formats: []string{FormatTt, FormatRock}, WithoutDeps: true,
	}))

	err := checkFormats(Options{Formats: []string{"zip"}})
	require.ErrorIs(t, err, errFormat)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))

	err = checkFormats(Options{Formats: []string{FormatRock}, WithoutDeps: true})
	require.ErrorIs(t, err, errArchiveOnly)
}

// TestReleaseDependencies: the rock declares the ranges of [dependencies] and
// of the components of the product, all the declarations of one rock
// together; a registry of its own is a warning, a path dependency an error.
func TestReleaseDependencies(t *testing.T) {
	t.Parallel()

	man := &manifest.Manifest{
		Package: manifest.Package{Name: "app"},
		Dependencies: map[string]manifest.Dependency{
			"luasocket": {Source: "registry", Version: ">=3.0.0"},
			"checks":    {Source: "registry", Version: "*"},
			"wasm":      {Source: "registry", Version: "0.1.0", Registry: "https://example.org/"},
		},
		Components: map[string]manifest.Component{
			"a": {Path: "a", Dependencies: map[string]manifest.Dependency{
				"luasocket": {Source: "registry", Version: "<4.0.0"},
			}},
			"b": {Path: "b", Dependencies: map[string]manifest.Dependency{
				"other": {Source: "path", Path: "../other"},
			}},
		},
		Products: map[string]manifest.Product{
			"default": {Components: []string{"a"}, Default: true},
			"full":    {Components: []string{"a", "b"}},
		},
	}

	got, warnings, err := releaseDependencies(man, "default")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"luasocket": ">= 3.0.0, < 4.0.0",
		"checks":    "",
		"wasm":      "== 0.1.0",
	}, got)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "wasm from https://example.org/")

	_, _, err = releaseDependencies(man, "full")
	require.ErrorIs(t, err, errPathDependency)
	assert.Contains(t, err.Error(), "other")
}

func TestSourceURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "git+https://github.com/x/y", sourceURL("https://github.com/x/y"))
	assert.Equal(t, "git+ssh://git@host/x/y.git", sourceURL("git+ssh://git@host/x/y.git"))
	assert.Equal(t, "git://host/x/y", sourceURL("git://host/x/y"))
	assert.Empty(t, sourceURL("git@github.com:x/y.git"))
	assert.Empty(t, sourceURL(""))
}

func TestRockPlatforms(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"linux", "macosx"},
		rockPlatforms([]string{"linux-amd64", "linux-arm64", "darwin-arm64"}))
	assert.Nil(t, rockPlatforms([]string{"linux-amd64", "any"}))
	assert.Nil(t, rockPlatforms(nil))
}
