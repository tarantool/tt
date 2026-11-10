package fetch_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
)

// member is an entry of a test archive.
type member struct {
	name     string
	typeflag byte
	body     string
}

// targz returns a .tar.gz archive of members, in order.
func targz(t *testing.T, members ...member) []byte {
	t.Helper()

	var buffer bytes.Buffer

	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)

	for _, entry := range members {
		header := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Mode: 0o755}
		if entry.typeflag == tar.TypeReg {
			header.Size = int64(len(entry.body))
		} else {
			header.Linkname = entry.body
		}

		require.NoError(t, archive.WriteHeader(header))

		if entry.typeflag == tar.TypeReg {
			_, err := archive.Write([]byte(entry.body))
			require.NoError(t, err)
		}
	}

	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())

	return buffer.Bytes()
}

func TestUnpackFile(t *testing.T) {
	t.Parallel()

	release := targz(t,
		member{name: "./", typeflag: tar.TypeDir},
		member{name: "./luafmt", typeflag: tar.TypeReg, body: "top"},
		member{name: "tool-1.0-linux/", typeflag: tar.TypeDir},
		member{name: "tool-1.0-linux/README.md", typeflag: tar.TypeReg, body: "readme"},
		member{name: "tool-1.0-linux/tool", typeflag: tar.TypeSymlink, body: "bin/tool"},
		member{name: "tool-1.0-linux/bin/tool", typeflag: tar.TypeReg, body: "program"},
		member{name: "other/bin/tool", typeflag: tar.TypeReg, body: "second"},
	)

	for name, want := range map[string]string{
		"luafmt":                  "top",
		"./luafmt":                "top",
		"tool":                    "program",
		"bin/tool":                "program",
		"./bin/tool":              "program",
		"tool-1.0-linux/bin/tool": "program",
		"other/bin/tool":          "second",
	} {
		got, err := fetch.UnpackFile(release, name, 0)
		require.NoError(t, err, name)
		assert.Equal(t, want, string(got), name)
	}

	// A part of a name is not a name.
	for _, name := range []string{"ool", "in/tool", "tool-1.0-linux", "missing"} {
		_, err := fetch.UnpackFile(release, name, 0)
		require.ErrorIs(t, err, fetch.ErrNotInArchive, name)
	}
}

func TestUnpackFileErrors(t *testing.T) {
	t.Parallel()

	archive := targz(t, member{name: "tool", typeflag: tar.TypeReg, body: "program"})

	got, err := fetch.UnpackFile(archive, "tool", 7)
	require.NoError(t, err)
	assert.Equal(t, "program", string(got))

	_, err = fetch.UnpackFile(archive, "tool", 6)
	require.ErrorIs(t, err, fetch.ErrTooLarge)

	_, err = fetch.UnpackFile([]byte("a plain text that is not gzip"), "tool", 0)
	require.ErrorIs(t, err, gzip.ErrHeader)

	var plain bytes.Buffer

	compressed := gzip.NewWriter(&plain)

	_, err = compressed.Write([]byte("not a tar archive, but long enough to be read as a header"))
	require.NoError(t, err)
	require.NoError(t, compressed.Close())

	_, err = fetch.UnpackFile(plain.Bytes(), "tool", 0)
	require.Error(t, err)
	require.NotErrorIs(t, err, fetch.ErrNotInArchive)
}
