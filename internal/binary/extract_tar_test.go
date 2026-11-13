package binary_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/untar"
)

// writeBundle writes a tar.gz archive of headers, a file of its name as the
// content of each regular one, into dir.
func writeBundle(t *testing.T, dir string, headers ...tar.Header) string {
	t.Helper()

	path := filepath.Join(dir, "bundle.tar.gz")

	archive, err := os.Create(path)
	require.NoError(t, err)

	compressed := gzip.NewWriter(archive)
	writer := tar.NewWriter(compressed)

	for _, header := range headers {
		if header.Typeflag == tar.TypeReg {
			header.Size = int64(len(header.Name))
		}

		require.NoError(t, writer.WriteHeader(&header))

		if header.Typeflag == tar.TypeReg {
			_, err := writer.Write([]byte(header.Name))
			require.NoError(t, err)
		}
	}

	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, archive.Close())

	return path
}

func TestExtractTar(t *testing.T) {
	dir := t.TempDir()

	bundle := writeBundle(t, dir,
		tar.Header{Typeflag: tar.TypeReg, Name: "tarantool-enterprise/tarantool", Mode: 0o755},
		tar.Header{Typeflag: tar.TypeDir, Name: "tarantool-enterprise/", Mode: 0o755},
		tar.Header{
			Typeflag: tar.TypeReg, Name: "tarantool-enterprise/include/tarantool/module.h",
			Mode: 0o644,
		},
	)
	require.NoError(t, binary.ExtractTar(bundle))

	content, err := os.ReadFile(filepath.Join(dir, "tarantool-enterprise", "tarantool"))
	require.NoError(t, err)
	assert.Equal(t, "tarantool-enterprise/tarantool", string(content))
	assert.FileExists(t, filepath.Join(dir, "tarantool-enterprise", "include", "tarantool",
		"module.h"))
}

func TestExtractTar_Refuses(t *testing.T) {
	cases := []struct {
		name   string
		header tar.Header
		err    error
	}{
		{"parent", tar.Header{Typeflag: tar.TypeReg, Name: "../x", Mode: 0o644},
			untar.ErrUnsafePath},
		{"absolute", tar.Header{Typeflag: tar.TypeReg, Name: "/x", Mode: 0o644},
			untar.ErrUnsafePath},
		{
			"symlink", tar.Header{Typeflag: tar.TypeSymlink, Name: "l", Linkname: "x", Mode: 0o777},
			untar.ErrUnknownType,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "download")
			require.NoError(t, os.Mkdir(dir, 0o755))

			bundle := writeBundle(t, dir, testCase.header)
			require.ErrorIs(t, binary.ExtractTar(bundle), testCase.err)
			assert.NoFileExists(t, filepath.Join(base, "x"))
		})
	}
}
