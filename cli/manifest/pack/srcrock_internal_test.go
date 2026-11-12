package pack

import (
	"archive/zip"
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/manif"

	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// writeGitLibrary writes the project of libraryManifest as a git repository
// whose HEAD is the tag v1.2.0, with a file git ignores and an untracked one
// beside the tracked ones.
func writeGitLibrary(t *testing.T) string {
	t.Helper()

	return writeGitPackage(t, nil)
}

// writeGitPackage is writeGitLibrary with files, by path, written over those
// of the library before the commit.
func writeGitPackage(t *testing.T, files map[string]string) string {
	t.Helper()

	project := map[string]string{
		manifestFileName:  libraryManifest,
		".gitignore":      "/_build/\n/.rocks/\n/_out/\n/app.manifest.lock\n*.o\n",
		"mylib/init.lua":  "return {}\n",
		"roles/mylib.lua": "return {}\n",
		"tools/gen.sh":    "#!/bin/sh\n",
	}
	maps.Copy(project, files)

	src := t.TempDir()
	writeTree(t, src, project)
	require.NoError(t, os.Chmod(filepath.Join(src, "tools", "gen.sh"), 0o755))

	git(t, src, "init", "-q")
	git(t, src, "add", "-A")
	git(t, src, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	git(t, src, "tag", "v1.2.0")

	return src
}

// TestPackSrcRock: the source rock holds the rockspec of the rock and, in
// the directory LuaRocks unpacks the source into, the files git tracks and
// a VERSION file of the version; the rockspec is the one of the rock and the
// one beside it, and the server lists the source rock.
func TestPackSrcRock(t *testing.T) {
	t.Parallel()

	src := writeGitLibrary(t)
	out := filepath.Join(src, "_out")

	result, err := Run(context.Background(), packOptions(src, out, FormatRock, FormatRockSrc))
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(out, "mylib-1.2.0-1.src.rock"), result.SrcRock)
	assert.Equal(t, filepath.Join(out, "mylib-1.2.0-1.all.rock"), result.Rock)

	entries := zipEntries(t, result.SrcRock)
	assert.Equal(t, []string{
		"mylib-1.2.0-1.rockspec",
		"mylib/.gitignore",
		"mylib/VERSION",
		"mylib/app.manifest.toml",
		"mylib/mylib/init.lua",
		"mylib/roles/mylib.lua",
		"mylib/tools/gen.sh",
	}, sortedKeys(entries))
	assert.Equal(t, "1.2.0\n", entries["mylib/VERSION"])

	rockspec, err := os.ReadFile(result.Rockspec)
	require.NoError(t, err)
	assert.Equal(t, string(rockspec), entries["mylib-1.2.0-1.rockspec"])
	assert.Equal(t, string(rockspec), zipEntries(t, result.Rock)["mylib-1.2.0-1.rockspec"])
	assert.Contains(t, string(rockspec), `tag = "v1.2.0",`)

	modes := zipModes(t, result.SrcRock)
	assert.Equal(t, srcExecMode, modes["mylib/tools/gen.sh"])
	assert.Equal(t, srcFileMode, modes["mylib/mylib/init.lua"])

	manifest, err := os.ReadFile(filepath.Join(out, "manifest"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), `arch = "src"`)
}

// TestPackSrcRockOfAWorkInProgress: the manifest is in the source rock even
// when git does not track it, and a pack whose components hold files git
// does not track warns of them, since the source rock leaves them out.
func TestPackSrcRockOfAWorkInProgress(t *testing.T) {
	t.Parallel()

	src := writeGitLibrary(t)
	git(t, src, "rm", "-q", "--cached", manifestFileName)
	git(t, src, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "untrack")
	writeTree(t, src, map[string]string{"roles/new.lua": "return {}\n", "notes.txt": "x"})

	var warnings []string

	opts := packOptions(src, filepath.Join(src, "_out"), FormatRockSrc)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	entries := zipEntries(t, result.SrcRock)
	assert.Contains(t, entries, "mylib/"+manifestFileName)
	assert.NotContains(t, entries, "mylib/roles/new.lua")

	var untracked []string

	for _, warning := range warnings {
		if strings.Contains(warning, "git does not track") {
			untracked = append(untracked, warning)
		}
	}

	require.Len(t, untracked, 1)
	assert.Contains(t, untracked[0], "roles/new.lua")
	assert.NotContains(t, untracked[0], "notes.txt")
}

// TestPackSrcRockIsReproducible: the same files are the same source rock.
func TestPackSrcRockIsReproducible(t *testing.T) {
	t.Parallel()

	src := writeGitLibrary(t)

	pack := func(out string) []byte {
		result, err := Run(context.Background(), packOptions(src, out, FormatRockSrc))
		require.NoError(t, err)
		assert.Empty(t, result.Rock)

		data, err := os.ReadFile(result.SrcRock)
		require.NoError(t, err)

		return data
	}

	assert.Equal(t, pack(filepath.Join(src, "_out", "a")), pack(filepath.Join(src, "_out", "b")))
}

// TestPackSrcRockOutsideGit: a project in no git repository is packed whole,
// but the directories tt keeps, and its VERSION is the one of the pack.
func TestPackSrcRockOutsideGit(t *testing.T) {
	t.Parallel()

	src := writeLibrary(t)
	writeTree(t, src, map[string]string{"_runtime/tarantool": "x"})

	result, err := Run(context.Background(),
		packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRockSrc))
	require.NoError(t, err)

	entries := zipEntries(t, result.SrcRock)
	assert.Equal(t, []string{
		"mylib-1.0.0-1.rockspec",
		"mylib/VERSION",
		"mylib/app.manifest.lock",
		"mylib/app.manifest.toml",
		"mylib/mylib/init.lua",
		"mylib/mylib/util.lua",
		"mylib/roles/mylib.lua",
	}, sortedKeys(entries))
	assert.Equal(t, "1.0.0\n", entries["mylib/VERSION"])
}

// TestSrcRockBuildsInAProject: a project depending on a rock its server has
// only the source rock of builds it from the manifest in it, at the revision
// of the release, and does not build it again the next time.
func TestSrcRockBuildsInAProject(t *testing.T) {
	t.Parallel()

	src := writeGitLibrary(t)
	out := filepath.Join(t.TempDir(), "server")

	opts := packOptions(src, out, FormatRockSrc)

	opts.Revision = 2

	_, err := Run(context.Background(), opts)
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
mylib = { version = '>=1.2.0', registry = '%s' }

[components.app]
path = '.'
include = ['init.lua']
`, out),
		versionFileName: "0.1.0\n",
		"init.lua":      "return require('mylib')\n",
	})

	buildConsumer := func() {
		t.Helper()

		require.NoError(t, build.Run(context.Background(), build.Options{
			ProjectDir: consumer,
			TtVersion:  "tt test",
			Tarantool:  rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
		}))
	}

	buildConsumer()

	share := filepath.Join(consumer, rocksDirName, "share", "tarantool")
	assert.FileExists(t, filepath.Join(share, "mylib", "init.lua"))
	assert.FileExists(t, filepath.Join(share, "mylib", "version.lua"))
	assert.FileExists(t, filepath.Join(share, "roles", "mylib.lua"))
	assert.DirExists(t, filepath.Join(share, "rocks", "mylib", "1.2.0-2"))

	lock, err := os.ReadFile(filepath.Join(consumer, lockFileName))
	require.NoError(t, err)
	assert.Contains(t, string(lock), "version = '1.2.0-2'")

	// The next build takes the rock it built: the source rock is not
	// unpacked again.
	unpacked := filepath.Join(consumer, buildDirName, "src", "mylib-1.2.0-2")
	require.NoError(t, os.RemoveAll(unpacked))

	buildConsumer()

	assert.NoDirExists(t, unpacked)
	assert.FileExists(t, filepath.Join(share, "mylib", "init.lua"))
}

// TestSrcRockInstallsTheLicenseFiles: a rock built from its source rock has
// the license files of the package in doc/ of its install dir, as the binary
// rock of the release has them: the same files, and the same doc section of
// the rock_manifest. The source rock carries a license file git does not
// track, as it carries the manifest. They are laid out apart from the stage
// of the build.
func TestSrcRockInstallsTheLicenseFiles(t *testing.T) {
	t.Parallel()

	src := writeGitPackage(t, map[string]string{
		manifestFileName: strings.Replace(libraryManifest, "license = 'BSD-2-Clause'\n",
			"license = 'BSD-2-Clause'\nlicense_files = ['LICENSE', 'licenses']\n", 1),
		"licenses/vendored.md": "the license of a vendored file\n",
	})
	writeTree(t, src, map[string]string{"LICENSE": "BSD-2-Clause text\n"})

	binary := filepath.Join(t.TempDir(), "binary")
	source := filepath.Join(t.TempDir(), "source")

	_, err := Run(context.Background(), packOptions(src, binary, FormatRock))
	require.NoError(t, err)

	result, err := Run(context.Background(), packOptions(src, source, FormatRockSrc))
	require.NoError(t, err)
	assert.Equal(t, "BSD-2-Clause text\n", zipEntries(t, result.SrcRock)["mylib/LICENSE"])

	// The untracked file makes the version dirty, that of both rocks alike.
	version := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(result.Rockspec), "mylib-"),
		rockspecExt)

	fromBinary := buildConsumer(t, binary)
	fromSource := buildConsumer(t, source)

	// The consumer of the source server built the rock from the source rock.
	built := filepath.Join(fromSource, buildDirName, "src", "mylib")
	assert.DirExists(t, filepath.Join(fromSource, buildDirName, "src", "mylib-"+version))
	assert.NoDirExists(t, filepath.Join(built, "rock", "default", rocktree.StageDoc))

	leftovers, err := filepath.Glob(filepath.Join(built, "rock-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)

	assert.Equal(t, map[string]string{
		"LICENSE":              "BSD-2-Clause text\n",
		"licenses/vendored.md": "the license of a vendored file\n",
	}, assertInstalledAlike(t, fromBinary, fromSource, version))
}

// TestSrcRockChecksTheLicenseFiles: a license file that is not there, or that
// leads out of the project, fails the pack of the source rock as it fails the
// binary rock and the archive.
func TestSrcRockChecksTheLicenseFiles(t *testing.T) {
	t.Parallel()

	for entry, want := range map[string]error{
		"NOTICE":       errMissingInclude,
		"../LICENSE":   errEscapingPath,
		"/etc/passwd":  errEscapingPath,
		"VERSION":      errReservedName,
		"_build/notes": errReservedName,
	} {
		src := writeGitPackage(t, map[string]string{
			manifestFileName: strings.Replace(libraryManifest, "license = 'BSD-2-Clause'\n",
				fmt.Sprintf("license = 'BSD-2-Clause'\nlicense_files = [%q]\n", entry), 1),
		})

		_, err := Run(context.Background(),
			packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRockSrc))
		require.ErrorIs(t, err, want, entry)
		assert.Contains(t, err.Error(), fmt.Sprintf("license_files entry %q", entry))
	}
}

// assertInstalledAlike asserts that the consumers want and got hold mylib at
// version with the same files in its install dir, the same doc/, and the
// same doc, lib and bin sections of its rock_manifest, and returns the files
// of its doc/ in want.
func assertInstalledAlike(t *testing.T, want, got, version string) map[string]string {
	t.Helper()

	installDir := func(consumer string) string {
		return filepath.Join(consumer, rocksDirName, "share", "tarantool", "rocks", "mylib",
			version)
	}

	docs := dirFiles(t, filepath.Join(installDir(want), rocktree.StageDoc))
	assert.Equal(t, docs, dirFiles(t, filepath.Join(installDir(got), rocktree.StageDoc)))

	names := func(consumer string) []string {
		return sortedKeys(dirFiles(t, installDir(consumer)))
	}
	assert.Equal(t, names(want), names(got))

	rockManifest := func(consumer string) *luarocks.RockManifest {
		rm, err := manif.FileStore{}.ReadRock(filepath.Join(installDir(consumer), "rock_manifest"))
		require.NoError(t, err)

		return rm
	}

	assert.Len(t, rockManifest(want).Doc, len(docs))
	assert.Equal(t, rockManifest(want).Doc, rockManifest(got).Doc)
	assert.Equal(t, rockManifest(want).Lib, rockManifest(got).Lib)
	assert.Equal(t, rockManifest(want).Bin, rockManifest(got).Bin)

	return docs
}

// TestPathDependencyInstallsTheLicenseFiles: a path dependency holding a
// manifest has the license files of its package in doc/ of its install dir,
// as the binary rock of the package has them, a license file that is a
// symlink to that of its monorepo the file it leads to.
func TestPathDependencyInstallsTheLicenseFiles(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	lib := filepath.Join(repo, "mylib")

	writeTree(t, repo, map[string]string{"COPYING": "BSD-2-Clause text\n"})
	writeTree(t, lib, map[string]string{
		manifestFileName: strings.Replace(libraryManifest, "license = 'BSD-2-Clause'\n",
			"license = 'BSD-2-Clause'\nlicense_files = ['LICENSE', 'licenses']\n", 1),
		versionFileName:        "1.0.0\n",
		"mylib/init.lua":       "return {}\n",
		"roles/mylib.lua":      "return {}\n",
		"licenses/vendored.md": "the license of a vendored file\n",
	})
	require.NoError(t, os.Symlink(filepath.Join("..", "COPYING"), filepath.Join(lib, "LICENSE")))

	server := filepath.Join(base, "server")

	_, err := Run(context.Background(), packOptions(lib, server, FormatRock))
	require.NoError(t, err)

	fromBinary := buildConsumer(t, server)

	fromPath := filepath.Join(base, "consumer")
	writeTree(t, fromPath, map[string]string{
		manifestFileName: `manifest_version = '0.1'

[package]
name = 'consumer'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
mylib = { source = 'path', path = '../repo/mylib' }

[components.app]
path = '.'
include = ['init.lua']
`,
		versionFileName: "0.1.0\n",
		"init.lua":      "return require('mylib')\n",
	})

	require.NoError(t, build.Run(context.Background(), build.Options{
		ProjectDir: fromPath,
		TtVersion:  "tt test",
		Tarantool:  rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
	}))

	assert.Equal(t, map[string]string{
		"LICENSE":              "BSD-2-Clause text\n",
		"licenses/vendored.md": "the license of a vendored file\n",
	}, assertInstalledAlike(t, fromBinary, fromPath, "1.0.0-1"))

	// The neighbour's directory is left as it was: no doc/, no _build/.
	assert.NoDirExists(t, filepath.Join(lib, rocktree.StageDoc))
	assert.NoDirExists(t, filepath.Join(lib, buildDirName, "path"))

	state := filepath.Join(fromPath, buildDirName, "path", "mylib")

	leftovers, err := filepath.Glob(filepath.Join(state, "rock-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

// dirFiles reads the files of the tree at dir by slash path relative to it.
func dirFiles(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := map[string]string{}
	tree := os.DirFS(dir)

	err := fs.WalkDir(tree, ".", func(path string, entry fs.DirEntry, err error) error {
		require.NoError(t, err)

		if entry.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(tree, path)
		require.NoError(t, err)

		files[path] = string(data)

		return nil
	})
	require.NoError(t, err)

	return files
}

// zipModes reads the modes of the entries of the zip archive at path.
func zipModes(t *testing.T, path string) map[string]os.FileMode {
	t.Helper()

	archive, err := zip.OpenReader(path)
	require.NoError(t, err)

	defer func() { _ = archive.Close() }()

	modes := map[string]os.FileMode{}
	for _, file := range archive.File {
		modes[file.Name] = file.Mode()
	}

	return modes
}

func TestSrcDirName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "memcached", srcDirName("git+https://github.com/tarantool/memcached.git", "x"))
	assert.Equal(t, "memcached", srcDirName("git+https://github.com/tarantool/memcached/", "x"))
	assert.Equal(t, "pkg", srcDirName("", "pkg"))
}
