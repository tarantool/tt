package rws

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/untar"
)

// debEntry is an entry of the data of a test package.
type debEntry struct {
	name string
	// kind is the tar type; 0 is a regular file.
	kind byte
	mode int64
	// data is the content of a file or the target of a link.
	data string
}

// tarFixture returns the uncompressed tar of entries.
func tarFixture(t *testing.T, entries ...debEntry) []byte {
	t.Helper()

	var buffer bytes.Buffer

	archive := tar.NewWriter(&buffer)

	for _, entry := range entries {
		header := &tar.Header{Typeflag: entry.kind, Name: entry.name, Mode: entry.mode}

		switch entry.kind {
		case 0, tar.TypeReg:
			header.Typeflag, header.Size = tar.TypeReg, int64(len(entry.data))
		case tar.TypeSymlink, tar.TypeLink:
			header.Linkname = entry.data
		}

		require.NoError(t, archive.WriteHeader(header))

		if header.Typeflag == tar.TypeReg {
			_, err := archive.Write([]byte(entry.data))
			require.NoError(t, err)
		}
	}

	require.NoError(t, archive.Close())

	return buffer.Bytes()
}

// gzipFixture compresses data with gzip.
func gzipFixture(t *testing.T, data []byte) []byte {
	t.Helper()

	var buffer bytes.Buffer

	compressed := gzip.NewWriter(&buffer)

	_, err := compressed.Write(data)
	require.NoError(t, err)
	require.NoError(t, compressed.Close())

	return buffer.Bytes()
}

// arMember is a member of a test ar archive.
type arMember struct {
	name string
	data []byte
}

// arFixture writes an ar archive of members as dpkg-deb does: the common
// format, with a header of 60 bytes for each member and the contents
// padded to an even size.
func arFixture(members ...arMember) []byte {
	var buffer bytes.Buffer

	buffer.WriteString(arMagic)

	for _, member := range members {
		fmt.Fprintf(&buffer, "%-16s%-12d%-6d%-6d%-8s%-10d`\n",
			member.name, 0, 0, 0, "100644", len(member.data))
		buffer.Write(member.data)

		if len(member.data)%2 != 0 {
			buffer.WriteByte('\n')
		}
	}

	return buffer.Bytes()
}

// debMembers returns the members of a deb of format 2.0 with the data of
// entries in data.tar.gz.
func debMembers(t *testing.T, entries ...debEntry) []arMember {
	t.Helper()

	return []arMember{
		{"debian-binary", []byte("2.0\n")},
		{"control.tar.gz", gzipFixture(t, tarFixture(t,
			debEntry{name: "./control", mode: 0o644, data: "Package: tarantool\n"}))},
		{"data.tar.gz", gzipFixture(t, tarFixture(t, entries...))},
	}
}

// debFixture returns a deb with the data of entries.
func debFixture(t *testing.T, entries ...debEntry) []byte {
	t.Helper()

	return arFixture(debMembers(t, entries...)...)
}

// extractDebFixture writes archive to a file and unpacks it into
// destination.
func extractDebFixture(t *testing.T, archive []byte, destination string, limit int64) error {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "test.deb")
	require.NoError(t, os.WriteFile(archivePath, archive, 0o600))

	return extractDeb(archivePath, destination, limit)
}

func TestExtractDeb(t *testing.T) {
	entries := []debEntry{
		{name: "./", kind: tar.TypeDir, mode: 0o755},
		{name: "./usr/", kind: tar.TypeDir, mode: 0o755},
		{name: "./usr/bin/", kind: tar.TypeDir, mode: 0o755},
		{name: "./usr/bin/tarantool", mode: 0o755, data: "executable"},
		// dpkg-deb lists the directories, but a file needs no entry of them.
		{name: "./usr/include/tarantool/lua.h", mode: 0o644, data: "lua"},
		{name: "./usr/include/tarantool/lua.hpp", kind: tar.TypeSymlink, data: "lua.h"},
	}

	for _, test := range []struct {
		name    string
		members []arMember
	}{
		{"dpkg-deb", debMembers(t, entries...)},
		{"GNU ar names", func() []arMember {
			members := debMembers(t, entries...)
			for index := range members {
				members[index].name += "/"
			}

			return members
		}()},
		{"members of another tool before the data", func() []arMember {
			members := debMembers(t, entries...)

			return []arMember{
				members[0], members[1], {"_gpgorigin", []byte("signature")}, members[2],
			}
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "unpacked")

			require.NoError(t,
				extractDebFixture(t, arFixture(test.members...), destination, maxDebDataSize))

			for name, expected := range map[string]string{
				"usr/bin/tarantool": "executable", "usr/include/tarantool/lua.h": "lua",
				"usr/include/tarantool/lua.hpp": "lua",
			} {
				content, err := os.ReadFile(filepath.Join(destination, name))
				require.NoError(t, err)
				assert.Equal(t, expected, string(content), name)
			}

			info, err := os.Stat(filepath.Join(destination, "usr/bin/tarantool"))
			require.NoError(t, err)
			assert.NotZero(t, info.Mode().Perm()&0o100, "the executable keeps its mode")

			link, err := os.Readlink(filepath.Join(destination, "usr/include/tarantool/lua.hpp"))
			require.NoError(t, err)
			assert.Equal(t, "lua.h", link)
		})
	}
}

// TestExtractDebUnsupportedCompression fails on a data member compressed
// other than with gzip, which no package of the repository is.
func TestExtractDebUnsupportedCompression(t *testing.T) {
	for _, name := range []string{
		"data.tar.xz", "data.tar.zst", "data.tar.bz2", "data.tar.lzma", "data.tar",
	} {
		t.Run(name, func(t *testing.T) {
			members := debMembers(t, debEntry{name: "./usr/bin/tarantool", mode: 0o755})

			members[2].name = name

			err := extractDebFixture(t, arFixture(members...), t.TempDir(), maxDebDataSize)
			require.ErrorIs(t, err, errInvalidDeb)
			require.ErrorContains(t, err,
				fmt.Sprintf("unsupported deb data compression %q, only data.tar.gz is", name))
		})
	}
}

func TestMalformedDeb(t *testing.T) {
	valid := debFixture(t, debEntry{name: "./usr/bin/tarantool", mode: 0o755, data: "binary"})
	members := debMembers(t, debEntry{name: "./usr/bin/tarantool", mode: 0o755, data: "binary"})
	data := members[2].data
	// The header of the first member, at 8, has the size at 48.
	const sizeField = len(arMagic) + arSizeOffset

	tests := map[string]struct {
		archive []byte
		message string
	}{
		"empty":            {nil, "no ar archive"},
		"no ar archive":    {[]byte("<html>not found</html>"), "no ar archive"},
		"truncated header": {valid[:len(arMagic)+20], "read the member header at 8"},
		"invalid header terminator": {func() []byte {
			archive := bytes.Clone(valid)

			archive[len(arMagic)+arHeaderSize-1] = ' '

			return archive
		}(), "invalid member header at 8"},
		"size no number": {func() []byte {
			archive := bytes.Clone(valid)
			copy(archive[sizeField:], "size      ")

			return archive
		}(), `invalid size of the member "debian-binary"`},
		"size beyond the archive": {func() []byte {
			archive := bytes.Clone(valid)
			copy(archive[sizeField:], "9999999999")

			return archive
		}(), `invalid size of the member "debian-binary"`},
		"data first": {arFixture(members[2], members[0], members[1]),
			`the first member is "data.tar.gz", not debian-binary`},
		"format 3": {arFixture(arMember{"debian-binary", []byte("3.0\n")}, members[2]),
			`unsupported deb format "3.0"`},
		"format too large": {arFixture(
			arMember{"debian-binary", bytes.Repeat([]byte("2"), maxDebFormatSize+1)},
			members[2]), "debian-binary exceeds 64 bytes"},
		"no data": {arFixture(members[0], members[1]), "no data.tar.gz"},
		"too many members": {arFixture(append([]arMember{members[0]}, func() []arMember {
			others := make([]arMember, 0, maxDebMembers)
			for range maxDebMembers {
				others = append(others, arMember{"_extra", []byte("x")})
			}

			return others
		}()...)...), "no data.tar.gz among the first 16 members"},
		"data no gzip": {arFixture(members[0], members[1],
			arMember{"data.tar.gz", []byte("text")}), "decompress data.tar.gz"},
		"truncated data": {arFixture(members[0], members[1],
			arMember{"data.tar.gz", data[:len(data)-1]}), "read data.tar.gz"},
		"data checksum": {arFixture(members[0], members[1], arMember{"data.tar.gz", func() []byte {
			corrupted := bytes.Clone(data)
			// The footer is the CRC-32 of the data, then its size.
			corrupted[len(corrupted)-8] ^= 1

			return corrupted
		}()}), "read data.tar.gz: gzip: invalid checksum"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := extractDebFixture(t, test.archive, t.TempDir(), maxDebDataSize)
			require.ErrorIs(t, err, errInvalidDeb)
			require.ErrorContains(t, err, test.message)
		})
	}

	err := extractDebFixture(t, arFixture(members[0], members[1],
		arMember{"data.tar.gz", gzipFixture(t, []byte("text"))}), t.TempDir(), maxDebDataSize)
	require.ErrorContains(t, err, "extract deb data: cannot read the archive")
}

// TestExtractDebUnsafe keeps the data of a package in its destination:
// the checks of untar apply.
func TestExtractDebUnsafe(t *testing.T) {
	for _, test := range []struct {
		entry debEntry
		want  error
	}{
		{debEntry{name: "../escaped", mode: 0o644, data: "bad"}, untar.ErrUnsafePath},
		{debEntry{name: "/escaped", mode: 0o644, data: "bad"}, untar.ErrUnsafePath},
		{debEntry{name: "./usr/../../escaped", mode: 0o644, data: "bad"}, untar.ErrUnsafePath},
		{debEntry{name: "link", kind: tar.TypeSymlink, data: "../outside"}, untar.ErrUnsafeLink},
		{debEntry{name: "link", kind: tar.TypeSymlink, data: "/etc"}, untar.ErrUnsafeLink},
		{debEntry{name: "hardlink", kind: tar.TypeLink, data: "file"}, untar.ErrUnknownType},
		{debEntry{name: "fifo", kind: tar.TypeFifo, mode: 0o644}, untar.ErrUnknownType},
		{debEntry{name: "device", kind: tar.TypeChar, mode: 0o644}, untar.ErrUnknownType},
	} {
		t.Run(test.entry.name+test.entry.data, func(t *testing.T) {
			workspace := t.TempDir()
			destination := filepath.Join(workspace, "destination")
			outside := filepath.Join(workspace, "outside")
			require.NoError(t, os.Mkdir(outside, 0o700))

			archive := debFixture(t, debEntry{name: "file", mode: 0o644, data: "file"}, test.entry)

			err := extractDebFixture(t, archive, destination, maxDebDataSize)
			require.ErrorIs(t, err, test.want)

			written, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Empty(t, written, "nothing is written outside the destination")
			assert.NoFileExists(t, filepath.Join(workspace, "escaped"))
		})
	}
}

// TestDebDataLimit bounds the unpacked data, the padding of the tar after
// its entries included.
func TestDebDataLimit(t *testing.T) {
	entries := []debEntry{{name: "./usr/bin/tarantool", mode: 0o755, data: "executable"}}
	size := int64(len(tarFixture(t, entries...)))
	archive := debFixture(t, entries...)

	require.NoError(t, extractDebFixture(t, archive, t.TempDir(), size))

	err := extractDebFixture(t, archive, t.TempDir(), size-1)
	require.ErrorIs(t, err, errInvalidDeb)
	require.ErrorContains(t, err, fmt.Sprintf("deb data exceeds %d bytes", size-1))

	err = extractDebFixture(t, archive, t.TempDir(), 100)
	require.ErrorIs(t, err, errInvalidDeb)
	require.ErrorContains(t, err, "deb data exceeds 100 bytes")
}

func FuzzDebDataMember(f *testing.F) {
	members := []arMember{
		{"debian-binary", []byte("2.0\n")}, {"control.tar.gz", []byte("control")},
		{"data.tar.gz", []byte("data")},
	}
	f.Add(arFixture(members...))
	f.Add(arFixture(members[0]))
	f.Add([]byte(arMagic))
	f.Fuzz(func(t *testing.T, archive []byte) {
		data, err := debDataMember(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return
		}

		// The member is inside the archive.
		content, err := io.ReadAll(data)
		require.NoError(t, err)
		require.LessOrEqual(t, len(content), len(archive))
	})
}
