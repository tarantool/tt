package coredump_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/coredump"
	"github.com/tarantool/tt/sdk/sdktest"
)

// writeArchive writes a tar.gz archive with the files of contents, by name,
// into dir and returns its path.
func writeArchive(t *testing.T, dir, name string, contents map[string]string) string {
	t.Helper()

	path := filepath.Join(dir, name)

	archive, err := os.Create(path)
	require.NoError(t, err)

	compressed := gzip.NewWriter(archive)
	writer := tar.NewWriter(compressed)

	for member, content := range contents {
		require.NoError(t, writer.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: member, Mode: 0o644, Size: int64(len(content)),
		}))

		_, err := writer.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, archive.Close())

	return path
}

func TestCommands(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, coredump.New, "coredump", "--help")
	require.NoError(t, got.Err)
	assert.Regexp(t, `\n  inspect\s+inspect tarantool coredump\n`, got.Stdout)
	assert.Regexp(t, `\n  pack\s+pack tarantool coredump into tar.gz archive\n`, got.Stdout)
	assert.Regexp(t, `\n  unpack\s+unpack tarantool coredump tar.gz archive\n`, got.Stdout)
}

func TestArguments(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"pack", "unpack", "inspect"} {
		got := sdktest.Run(t, coredump.New, "coredump", command)
		require.ErrorContains(t, got.Err, "accepts 1 arg(s), received 0", command)
	}
}

func TestPackNoSuchFile(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, coredump.New, "coredump", "pack", "-e", "/bin/true",
		filepath.Join(t.TempDir(), "core"))
	require.ErrorContains(t, got.Err, "pack script execution failed")
}

//nolint:paralleltest // Unpack extracts into the working directory.
func TestUnpack(t *testing.T) {
	dir := t.TempDir()
	archive := writeArchive(t, dir, "tarantool-core-N-202611230344-host.tar.gz",
		map[string]string{
			"tarantool-core-N-202611230344-host/version": "Tarantool 3.9.0-0-g8b9a0f59a95b\n",
		})

	t.Chdir(dir)

	got := sdktest.Run(t, coredump.New, "coredump", "unpack", archive)
	require.NoError(t, got.Err)
	assert.Equal(t, "Archive was successfully unpacked.", got.Records[0].Message)

	content, err := os.ReadFile(filepath.Join(dir, "tarantool-core-N-202611230344-host",
		"version"))
	require.NoError(t, err)
	assert.Equal(t, "Tarantool 3.9.0-0-g8b9a0f59a95b\n", string(content))

	got = sdktest.Run(t, coredump.New, "coredump", "unpack", filepath.Join(dir, "missing"))
	require.ErrorContains(t, got.Err, "failed to unpack")
}

func TestInspectNoSuchFile(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, coredump.New, "coredump", "inspect", filepath.Join(t.TempDir(), "x"))
	require.ErrorContains(t, got.Err, "failed to inspect")
}

// TestInspectExperimental checks that inspect takes --experimental and goes
// on to open what it is given with it.
func TestInspectExperimental(t *testing.T) {
	t.Parallel()

	got := sdktest.Run(t, coredump.New, "coredump", "inspect", "--experimental",
		filepath.Join(t.TempDir(), "x"))
	require.ErrorContains(t, got.Err, "failed to inspect")
}
