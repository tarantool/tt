package pack

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// writeGitLibrary writes the project of libraryManifest as a git repository
// whose HEAD is the tag v1.2.0, with a file git ignores and an untracked one
// beside the tracked ones.
func writeGitLibrary(t *testing.T) string {
	t.Helper()

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName:  libraryManifest,
		".gitignore":      "/_build/\n/.rocks/\n/_out/\n/app.manifest.lock\n*.o\n",
		"mylib/init.lua":  "return {}\n",
		"roles/mylib.lua": "return {}\n",
		"tools/gen.sh":    "#!/bin/sh\n",
	})
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
