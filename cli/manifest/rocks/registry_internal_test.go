package rocks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindRockspecSingle returns the sole top-level rockspec.
func TestFindRockspecSingle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	spec := filepath.Join(dir, "mymod-1.0-1.rockspec")
	require.NoError(t, os.WriteFile(spec, []byte("package = 'mymod'\n"), 0o600))

	got, err := findRockspec(dir)
	require.NoError(t, err)
	assert.Equal(t, spec, got)
}

// TestFindRockspecNone reports errNoRockspec so LocalMetadata can treat the
// directory as a leaf.
func TestFindRockspecNone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.lua"), []byte("return {}\n"), 0o600))

	_, err := findRockspec(dir)
	require.ErrorIs(t, err, errNoRockspec)
}

// TestFindRockspecMultiple errors instead of silently pinning an arbitrary
// (alphabetically-first) rockspec when the directory ships more than one.
func TestFindRockspecMultiple(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "mymod-1.0-1.rockspec"), []byte("package = 'mymod'\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "mymod-2.0-1.rockspec"), []byte("package = 'mymod'\n"), 0o600))

	_, err := findRockspec(dir)
	require.ErrorIs(t, err, errMultipleRockspec)
	// It is not the leaf sentinel: an ambiguous directory is a real failure.
	assert.NotErrorIs(t, err, errNoRockspec)
}
