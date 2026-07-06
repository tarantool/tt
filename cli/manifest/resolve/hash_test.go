//nolint:testpackage // white-box: exercises the unexported contentHash directly.
package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContentHashFromDirectoryContents pins the content-hash contract: it is a
// function of the files' relative paths and bytes, stable across identical
// trees and sensitive to any change in content, a file's name, or the set of
// files.
func TestContentHashFromDirectoryContents(t *testing.T) {
	t.Parallel()

	write := func(dir, rel, content string) {
		path := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	base := t.TempDir()
	write(base, "init.lua", "return {}\n")
	write(base, "lib/util.lua", "return 1\n")

	baseHash, err := contentHash(base)
	require.NoError(t, err)
	assert.Contains(t, baseHash, "sha256:")

	// Identical contents in a different directory hash the same.
	twin := t.TempDir()
	write(twin, "init.lua", "return {}\n")
	write(twin, "lib/util.lua", "return 1\n")

	twinHash, err := contentHash(twin)
	require.NoError(t, err)
	assert.Equal(t, baseHash, twinHash)

	// Changing a byte changes the hash.
	changed := t.TempDir()
	write(changed, "init.lua", "return {}\n")
	write(changed, "lib/util.lua", "return 2\n")

	changedHash, err := contentHash(changed)
	require.NoError(t, err)
	assert.NotEqual(t, baseHash, changedHash)

	// Adding a file changes the hash.
	extra := t.TempDir()
	write(extra, "init.lua", "return {}\n")
	write(extra, "lib/util.lua", "return 1\n")
	write(extra, "extra.lua", "\n")

	extraHash, err := contentHash(extra)
	require.NoError(t, err)
	assert.NotEqual(t, baseHash, extraHash)
}

// TestContentHashSkipsSymlinks checks that a symlink - including one pointing at
// a directory, which would otherwise be read as a file and error - is skipped,
// not followed, so a valid tree still hashes.
func TestContentHashSkipsSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.lua"), []byte("return 1\n"), 0o600))
	require.NoError(t, os.MkdirAll(sub, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "b.lua"), []byte("return 2\n"), 0o600))

	before, err := contentHash(dir)
	require.NoError(t, err)

	// A symlink to the subdirectory must not abort the hash, and must not change
	// it (symlinks are skipped, not part of the content).
	require.NoError(t, os.Symlink(sub, filepath.Join(dir, "link")))

	after, err := contentHash(dir)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// TestContentHashTracksExecutableBit checks that flipping a file's executable
// bit changes the hash, so a path dependency's build script going +x is not
// invisible to the lock and staleness.
func TestContentHashTracksExecutableBit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	script := filepath.Join(dir, "build.sh")
	require.NoError(t, os.WriteFile(script, []byte("echo hi\n"), 0o600))

	plain, err := contentHash(dir)
	require.NoError(t, err)

	require.NoError(t, os.Chmod(script, 0o700)) //nolint:gosec // the test sets the executable bit

	executable, err := contentHash(dir)
	require.NoError(t, err)

	assert.NotEqual(t, plain, executable)
}
