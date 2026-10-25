package binary_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestReplaceLink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "app")

	require.NoError(t, binary.ReplaceLink(link, "../versions/app/app_1.0.0"))

	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, "../versions/app/app_1.0.0", target)

	require.NoError(t, binary.ReplaceLink(link, "../versions/app/app_2.0.0"))

	target, err = os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, "../versions/app/app_2.0.0", target)

	// Nothing but the link is left in the directory: the temporary link the
	// replacement goes through is renamed over it.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "app", entries[0].Name())

	require.NoError(t, binary.ReplaceLink(link, ""))
	assert.NoFileExists(t, link)

	require.NoError(t, binary.ReplaceLink(link, ""), "removing a missing link is no error")
}
