package build_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build/v3"
)

func TestCopyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "tool")

	require.NoError(t, os.WriteFile(src, []byte("#!/bin/sh\n"), 0o700))

	dst := filepath.Join(dir, "out", "bin", "tool")
	require.NoError(t, build.CopyFile(dst, src))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n", string(data))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	// A copy over an older one replaces it.
	require.NoError(t, os.WriteFile(src, []byte("new"), 0o600))
	require.NoError(t, build.CopyFile(dst, src))

	data, err = os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))

	err = build.CopyFile(dst, filepath.Join(dir, "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLookupEnv(t *testing.T) {
	t.Parallel()

	environ := []string{"PATH=/bin", "CC=gcc", "PATH=/opt/bin:/bin", "EMPTY="}

	value, ok := build.LookupEnv(environ, "PATH")
	assert.True(t, ok)
	assert.Equal(t, "/opt/bin:/bin", value)

	value, ok = build.LookupEnv(environ, "EMPTY")
	assert.True(t, ok)
	assert.Empty(t, value)

	_, ok = build.LookupEnv(environ, "C")
	assert.False(t, ok)
}
