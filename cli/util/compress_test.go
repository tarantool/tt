package util_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/untar"
)

func TestExtractTgz(t *testing.T) {
	tempDir := t.TempDir()
	require.NoError(t, util.ExtractTarGz(filepath.Join("testdata", "arch.tgz"), tempDir))

	/* Archive file tree:
	.
	├── file_link -> file.sh
	├── file.sh
	└── subdir
	    └── file.txt
	*/
	stat, err := os.Stat(filepath.Join(tempDir, "test_archive", "file.sh"))
	require.NoError(t, err)
	assert.NotZero(t, stat.Mode().Perm()&0o100) // Executable bit is set.

	linkTarget, err := os.Readlink(filepath.Join(tempDir, "test_archive", "file_link"))
	require.NoError(t, err)
	assert.Equal(t, "file.sh", linkTarget)
	assert.FileExists(t, filepath.Join(tempDir, "test_archive", "file_link"))
	assert.FileExists(t, filepath.Join(tempDir, "test_archive", "subdir", "file.txt"))
}

// TestExtractTgzRefusesOutside checks that an archive cannot write outside
// the destination, by a name or through a link of its own.
func TestExtractTgzRefusesOutside(t *testing.T) {
	cases := []struct {
		name    string
		headers []tar.Header
		err     error
	}{
		{"parent", []tar.Header{{Typeflag: tar.TypeReg, Name: "../x", Mode: 0o644}},
			untar.ErrUnsafePath},
		{"absolute", []tar.Header{{Typeflag: tar.TypeReg, Name: "/x", Mode: 0o644}},
			untar.ErrUnsafePath},
		{
			"symlink out", []tar.Header{
				{Typeflag: tar.TypeSymlink, Name: "out", Linkname: "..", Mode: 0o777},
				{Typeflag: tar.TypeReg, Name: "out/x", Mode: 0o644},
			},
			untar.ErrUnsafeLink,
		},
		{
			"absolute symlink", []tar.Header{
				{Typeflag: tar.TypeSymlink, Name: "out", Linkname: "/tmp", Mode: 0o777},
			},
			untar.ErrUnsafeLink,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			base := t.TempDir()
			dst := filepath.Join(base, "dst")
			archivePath := filepath.Join(base, "template.tgz")

			archive, err := os.Create(archivePath)
			require.NoError(t, err)

			compressed := gzip.NewWriter(archive)
			writer := tar.NewWriter(compressed)

			for _, header := range testCase.headers {
				require.NoError(t, writer.WriteHeader(&header))
			}

			require.NoError(t, writer.Close())
			require.NoError(t, compressed.Close())
			require.NoError(t, archive.Close())

			require.ErrorIs(t, util.ExtractTarGz(archivePath, dst), testCase.err)
			assert.NoFileExists(t, filepath.Join(base, "x"))
		})
	}
}

func TestExtractTgzErrors(t *testing.T) {
	tempDir := t.TempDir()
	require.Error(t, util.ExtractTarGz("non_existing_file", tempDir))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "text_file.tgz"), []byte("text"),
		os.FileMode(0o664)))
	require.Error(t, util.ExtractTarGz(filepath.Join(tempDir, "text_file.tgz"), tempDir))
}
