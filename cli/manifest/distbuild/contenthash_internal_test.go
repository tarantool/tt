package distbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes data to path, creating the directories on the way.
func writeFile(t *testing.T, path, data string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

// sha256Hex is the hex SHA-256 of data.
func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))

	return hex.EncodeToString(sum[:])
}

// listingHash is the content hash of files, given by path with their
// contents, computed the way the documentation of contentHash spells it.
func listingHash(files [][2]string) string {
	var listing strings.Builder

	for _, file := range files {
		_, _ = fmt.Fprintf(&listing, "%s  %s\n", sha256Hex(file[1]), file[0])
	}

	return contentHashPrefix + sha256Hex(listing.String())
}

// TestContentHashCoversTheFilesOfTheModule checks which files the hash of a
// module covers - the ones the bundle copies - and the listing it hashes.
func TestContentHashCoversTheFilesOfTheModule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	work := filepath.Join(dir, "work")

	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, "m.go"), "package m\n")
	writeFile(t, filepath.Join(dir, "lua", "init.lua"), "return {}\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "/tt\n")
	// Left out: what .gitignore ignores, VCS, vendor, a nested module, the
	// build's directory, links.
	writeFile(t, filepath.Join(dir, "tt"), "a binary\n")
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/x\n")
	writeFile(t, filepath.Join(dir, "vendor", "modules.txt"), "# x\n")
	writeFile(t, filepath.Join(dir, "nested", "go.mod"), "module example.com/m/nested\n")
	writeFile(t, filepath.Join(dir, "nested", "n.go"), "package nested\n")
	writeFile(t, filepath.Join(work, "main.go"), "package main\n")
	require.NoError(t, os.Symlink("m.go", filepath.Join(dir, "link.go")))

	got, err := contentHash([]hashRoot{{dir: dir, prefix: ""}}, dir, work)
	require.NoError(t, err)

	assert.Equal(t, listingHash([][2]string{
		{".gitignore", "/tt\n"},
		{"go.mod", "module example.com/m\n"},
		{"lua/init.lua", "return {}\n"},
		{"m.go", "package m\n"},
	}), got)
}

// TestContentHashFollowsTheContent checks that the hash changes with the
// content and the paths of the files and with nothing else.
func TestContentHashFollowsTheContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, "m.go"), "package m\n")

	hash := func() string {
		t.Helper()

		sum, err := contentHash([]hashRoot{{dir: dir, prefix: ""}}, dir, "")
		require.NoError(t, err)

		return sum
	}

	before := hash()

	require.NoError(t, os.Chmod(filepath.Join(dir, "m.go"), 0o755))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "m.go"), time.Unix(1, 0), time.Unix(1, 0)))
	assert.Equal(t, before, hash(), "modes and times are not content")

	writeFile(t, filepath.Join(dir, "m.go"), "package m // changed\n")
	assert.NotEqual(t, before, hash())

	writeFile(t, filepath.Join(dir, "m.go"), "package m\n")
	assert.Equal(t, before, hash())

	require.NoError(t, os.Rename(filepath.Join(dir, "m.go"), filepath.Join(dir, "n.go")))
	assert.NotEqual(t, before, hash(), "a path is content")
}

// TestContentHashOfSeveralRoots checks that the files of several roots are
// listed under their prefixes in one order.
func TestContentHashOfSeveralRoots(t *testing.T) {
	t.Parallel()

	core := t.TempDir()
	sdk := filepath.Join(core, "sdk")
	other := t.TempDir()

	writeFile(t, filepath.Join(core, "go.mod"), "module example.com/core\n")
	writeFile(t, filepath.Join(sdk, "go.mod"), "module example.com/core/sdk\n")
	writeFile(t, filepath.Join(other, "go.mod"), "module example.com/other\n")

	got, err := contentHash([]hashRoot{
		{dir: core, prefix: ""},
		{dir: sdk, prefix: "sdk"},
		{dir: other, prefix: "external/example.com/other"},
	}, core, "")
	require.NoError(t, err)

	assert.Equal(t, listingHash([][2]string{
		{"external/example.com/other/go.mod", "module example.com/other\n"},
		{"go.mod", "module example.com/core\n"},
		{"sdk/go.mod", "module example.com/core/sdk\n"},
	}), got)
}
