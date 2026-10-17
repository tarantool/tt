package install

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

type cpioEntry struct {
	name  string
	data  string
	mode  uint32
	ino   uint32
	nlink uint32
}

func cpioFixture(entries ...cpioEntry) []byte {
	var buffer bytes.Buffer

	entries = append(entries, cpioEntry{name: "TRAILER!!!"})

	for _, entry := range entries {
		nlink := entry.nlink
		if nlink == 0 {
			nlink = 1
		}

		fmt.Fprintf(&buffer, "070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
			entry.ino, entry.mode, 0, 0, nlink, 0, len(entry.data), 0, 0, 0, 0,
			len(entry.name)+1, 0)
		buffer.WriteString(entry.name)
		buffer.WriteByte(0)

		for buffer.Len()%4 != 0 {
			buffer.WriteByte(0)
		}

		buffer.WriteString(entry.data)

		for buffer.Len()%4 != 0 {
			buffer.WriteByte(0)
		}
	}

	return buffer.Bytes()
}

func rpmFixture(t *testing.T, compression string, payload []byte) []byte {
	t.Helper()

	var compressed bytes.Buffer

	if compression == "xz" {
		writer, err := (xz.WriterConfig{DictCap: 1 << 20}).NewWriter(&compressed)
		if err != nil {
			t.Fatal(err)
		}

		_, err = writer.Write(payload)
		if err != nil {
			t.Fatal(err)
		}

		err = writer.Close()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		compressed.Write(payload)
	}

	lead := make([]byte, 96, 112)
	copy(lead, []byte{0xed, 0xab, 0xee, 0xdb, 3})

	header := []byte{0x8e, 0xad, 0xe8, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	lead = append(lead, header...)

	result := lead // Empty signature is already aligned to 8.
	data := []byte("cpio\x00" + compression + "\x00")

	main := append([]byte(nil), header...)
	binary.BigEndian.PutUint32(main[8:12], 2)
	//nolint:gosec // The test compressor names are fixed, short literals.
	binary.BigEndian.PutUint32(main[12:16], uint32(len(data)))

	for i, tag := range []uint32{1124, 1125} {
		entry := make([]byte, 16)
		binary.BigEndian.PutUint32(entry[:4], tag)
		binary.BigEndian.PutUint32(entry[4:8], 6)
		binary.BigEndian.PutUint32(entry[8:12], uint32(i*5))
		binary.BigEndian.PutUint32(entry[12:16], 1)

		main = append(main, entry...)
	}

	result = append(result, main...)
	result = append(result, data...)

	return append(result, compressed.Bytes()...)
}

func extractFixture(t *testing.T, archive []byte, destination string) error {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "test.rpm")

	err := os.WriteFile(archivePath, archive, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return extractRPM(archivePath, destination)
}

func TestExtractRPM(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "unpacked")
	payload := cpioFixture(
		cpioEntry{name: "./usr", mode: 0o040755},
		cpioEntry{name: "./usr/bin/tool", mode: 0o100755, data: "executable"},
		cpioEntry{name: "./usr/include/tool.h", mode: 0o100644, data: "header"},
		cpioEntry{name: "./usr/bin/alias", mode: 0o120777, data: "tool"},
		cpioEntry{name: "./usr/include/link-first", mode: 0o100644, ino: 1, nlink: 2},
		cpioEntry{
			name: "./usr/include/link-last", mode: 0o100644,
			ino: 1, nlink: 2, data: "shared",
		},
	)

	err := extractFixture(t, rpmFixture(t, "xz", payload), destination)
	if err != nil {
		t.Fatal(err)
	}

	for name, expected := range map[string]string{
		"usr/bin/tool": "executable", "usr/bin/alias": "executable",
		"usr/include/tool.h": "header", "usr/include/link-first": "shared",
		"usr/include/link-last": "shared",
	} {
		contents, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil || string(contents) != expected {
			t.Fatalf("%s: content %q, error %v", name, contents, err)
		}
	}

	info, err := os.Stat(filepath.Join(destination, "usr/bin/tool"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("executable permissions: %v, %v", info, err)
	}

	first, err := os.Stat(filepath.Join(destination, "usr/include/link-first"))
	if err != nil {
		t.Fatal(err)
	}

	last, err := os.Stat(filepath.Join(destination, "usr/include/link-last"))
	if err != nil || !os.SameFile(first, last) {
		t.Fatalf("hardlinks do not share an inode: %v", err)
	}

	link, err := os.Readlink(filepath.Join(destination, "usr/bin/alias"))
	if err != nil || link != "tool" {
		t.Fatalf("symlink: %q, %v", link, err)
	}
}

func TestRejectUnsupportedRPMCompression(t *testing.T) {
	for _, compression := range []string{"gzip", "bzip2", "zstd", "none", "unknown", ""} {
		t.Run(compression, func(t *testing.T) {
			archive := rpmFixture(t, compression, cpioFixture())
			err := extractFixture(t, archive, t.TempDir())

			if err == nil || !strings.Contains(err.Error(), "unsupported RPM payload compression") {
				t.Fatalf("expected unsupported compression error, got %v", err)
			}
		})
	}
}

func TestTruncatedCPIO(t *testing.T) {
	payload := cpioFixture(cpioEntry{name: "file", mode: 0o100644, data: "hello"})
	for name, truncated := range map[string][]byte{
		"file data":       payload[:cpioHeaderSize+len("file")+3],
		"missing trailer": payload[:len(payload)-len(cpioFixture())],
	} {
		t.Run(name, func(t *testing.T) {
			err := extractFixture(t, rpmFixture(t, "xz", truncated), t.TempDir())
			if err == nil {
				t.Fatal("truncated CPIO accepted")
			}
		})
	}
}

func TestRejectUnsafePaths(t *testing.T) {
	for _, entry := range []cpioEntry{
		{name: "../escaped", mode: 0o100644, data: "bad"},
		{name: "/escaped", mode: 0o100644, data: "bad"},
		{name: "dir/link", mode: 0o120777, data: "../../escaped"},
		{name: "link", mode: 0o120777, data: "/tmp/escaped"},
		{name: "device", mode: 0o020600},
	} {
		t.Run(entry.name+entry.data, func(t *testing.T) {
			err := extractFixture(t, rpmFixture(t, "xz", cpioFixture(entry)), t.TempDir())
			if err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}

func TestRejectExistingSymlinkEscape(t *testing.T) {
	destination, outside := t.TempDir(), t.TempDir()

	err := os.Symlink(outside, filepath.Join(destination, "link"))
	if err != nil {
		t.Fatal(err)
	}

	payload := cpioFixture(cpioEntry{name: "link/escaped", mode: 0o100644, data: "bad"})

	err = extractFixture(t, rpmFixture(t, "xz", payload), destination)
	if err == nil {
		t.Fatal("symlink escape accepted")
	}

	_, statErr := os.Stat(filepath.Join(outside, "escaped"))

	if !os.IsNotExist(statErr) {
		t.Fatalf("wrote outside extraction directory: %v", statErr)
	}
}

func TestMalformedRPM(t *testing.T) {
	payload := cpioFixture(cpioEntry{name: "file", mode: 0o100644, data: "hello"})
	valid := rpmFixture(t, "xz", payload)
	cases := map[string]func([]byte) []byte{
		"lead magic":      func(b []byte) []byte { b[0] = 0; return b },
		"signature magic": func(b []byte) []byte { b[96] = 0; return b },

		"huge header": func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[104:108], ^uint32(0))

			return b
		},
		"main magic": func(b []byte) []byte { b[112] = 0; return b },

		"tag offset": func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[136:140], 1<<20)

			return b
		},

		"negative tag offset": func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[136:140], ^uint32(0))

			return b
		},

		"duplicate payload tag": func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[144:148], rpmPayloadFormatTag)

			return b
		},

		"payload string count": func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[140:144], ^uint32(0))

			return b
		},

		"tag type": func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[132:136], 4)

			return b
		},
		"tag string": func(b []byte) []byte { b[164] = 'x'; b[169] = 'x'; return b },

		"unsupported compression": func(b []byte) []byte {
			copy(b[165:167], "no")

			return b
		},
		"truncated payload": func(b []byte) []byte { return b[:len(b)-1] },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			err := extractFixture(t, mutate(bytes.Clone(valid)), t.TempDir())
			if err == nil {
				t.Fatal("malformed RPM accepted")
			}
		})
	}

	for _, n := range []int{0, 4, 95, 100, 120, 160} {
		t.Run(fmt.Sprintf("truncated-%d", n), func(t *testing.T) {
			err := extractFixture(t, valid[:n], t.TempDir())
			if err == nil {
				t.Fatal("truncated RPM accepted")
			}
		})
	}
}

func TestMalformedCPIO(t *testing.T) {
	// newc fields after the six-byte magic are eight-digit hexadecimal values.
	const (
		fileSizeOffset = len(cpioMagicNewc) + 6*cpioHexFieldSize
		nameSizeOffset = len(cpioMagicNewc) + 11*cpioHexFieldSize
	)

	valid := cpioFixture(cpioEntry{name: "file", mode: 0o100644, data: "hello"})
	cases := map[string]func([]byte){
		"invalid magic": func(payload []byte) { payload[0] = 'x' },
		"zero filename length": func(payload []byte) {
			copy(payload[nameSizeOffset:], "00000000")
		},
		"excessive filename length": func(payload []byte) {
			copy(payload[nameSizeOffset:], "ffffffff")
		},
		"excessive file size": func(payload []byte) {
			copy(payload[fileSizeOffset:], fmt.Sprintf("%08x", maxRPMPayloadSize+1))
		},
		"invalid hex field": func(payload []byte) {
			payload[len(cpioMagicNewc)] = 'x'
		},
		"unterminated filename": func(payload []byte) {
			payload[cpioHeaderSize+len("file")] = 'x'
		},
		"embedded NUL in filename": func(payload []byte) {
			payload[cpioHeaderSize] = 0
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			payload := bytes.Clone(valid)
			mutate(payload)

			err := extractFixture(t, rpmFixture(t, "xz", payload), t.TempDir())
			if err == nil {
				t.Fatal("malformed CPIO accepted")
			}
		})
	}
}

func TestRejectArchiveSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	destination := filepath.Join(workspace, "destination")
	outside := filepath.Join(workspace, "outside")

	err := os.Mkdir(outside, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	payload := cpioFixture(
		cpioEntry{name: "link", mode: 0o120777, data: "../outside"},
		cpioEntry{name: "link/proof.txt", mode: 0o100644, data: "controlled audit marker"},
	)

	err = extractFixture(t, rpmFixture(t, "xz", payload), destination)
	if err == nil {
		t.Fatal("archive symlink escape accepted")
	}

	_, err = os.Stat(filepath.Join(outside, "proof.txt"))
	if !os.IsNotExist(err) {
		t.Fatalf("wrote outside extraction directory: %v", err)
	}
}

func TestRejectExcessiveDecoderWindow(t *testing.T) {
	payload := cpioFixture(cpioEntry{name: "file", mode: 0o100644, data: "hello"})

	// XZ has a twelve-byte stream header, then a block header whose length is
	// (first byte + 1) * 4. For the writer's single LZMA2 filter, the dictionary
	// property follows the size, flags, filter ID and property size bytes.
	// https://github.com/tukaani-project/xz/blob/master/doc/xz-file-format.txt
	// Sections 2.1, 3.1 and 5.4 describe these fields.
	const (
		xzStreamHeaderSize = 12
		xzHeaderAlignment  = 4
		xzDictProperty     = 4
		xzMaxDictProperty  = 40 // LZMA2 dictionary of 2^32 - 1 bytes.
	)

	archive := rpmFixture(t, "xz", payload)
	xzStart := bytes.Index(archive, []byte("\xfd7zXZ\x00"))

	if xzStart < 0 {
		t.Fatal("XZ stream header missing from fixture")
	}

	blockStart := xzStart + xzStreamHeaderSize
	blockSize := (int(archive[blockStart]) + 1) * xzHeaderAlignment
	block := archive[blockStart : blockStart+blockSize]

	block[xzDictProperty] = xzMaxDictProperty
	binary.LittleEndian.PutUint32(block[len(block)-crc32.Size:],
		crc32.ChecksumIEEE(block[:len(block)-crc32.Size]))

	err := extractFixture(t, archive, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "dictionary size exceeds max") {
		t.Fatalf("expected dictionary limit error, got %v", err)
	}
}

func TestRPMPayloadLimitIncludesTrailingData(t *testing.T) {
	const budget = 1024

	payload := append(cpioFixture(), make([]byte, budget)...)
	archive := rpmFixture(t, "xz", payload)
	xzStart := bytes.Index(archive, []byte("\xfd7zXZ\x00"))
	_, err := newRPMXZReader(bytes.NewReader(archive[xzStart:]), maxRPMDecoderWindow, budget)

	if err == nil || !strings.Contains(err.Error(), "RPM payload exceeds") {
		t.Fatalf("expected payload size limit error, got %v", err)
	}
}

func TestCPIOEntryLimit(t *testing.T) {
	// Repeated root directory records exercise the entry budget without
	// creating thousands of files or consuming significant disk space.
	directory := cpioFixture(cpioEntry{name: ".", mode: 0o40755})
	trailer := cpioFixture()
	entry := directory[:len(directory)-len(trailer)]
	payload := append(bytes.Repeat(entry, maxCPIOEntries+1), trailer...)

	err := extractFixture(t, rpmFixture(t, "xz", payload), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("expected entry limit error, got %v", err)
	}
}

func FuzzReadRPMHeader(f *testing.F) {
	// Independent wire-format seeds: an empty header and a STRING payload tag.
	f.Add([]byte{0x8e, 0xad, 0xe8, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{
		0x8e, 0xad, 0xe8, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 5,
		0, 0, 4, 0x64, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0, 1,
		'c', 'p', 'i', 'o', 0,
	})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		// Header reads allocate only for supplied data, not declared lengths.
		if len(encoded) > 64<<10 {
			return
		}

		_, _ = readRPMHeader(bytes.NewReader(encoded))
	})
}

func FuzzReadCPIOHeader(f *testing.F) {
	payload := cpioFixture(cpioEntry{name: "file", mode: 0o100644, data: "contents"})
	f.Add(payload[:cpioHeaderSize])
	f.Add([]byte("070702"))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > cpioHeaderSize {
			return
		}

		_, _ = readCPIOHeader(bytes.NewReader(encoded))
	})
}

func FuzzXZPayload(f *testing.F) {
	// Bound both allocation and output independently of attacker-controlled
	// dictionary properties, repetition counts and concatenated streams.
	const budget = 1 << 20

	var seed bytes.Buffer

	writer, err := (xz.WriterConfig{DictCap: budget}).NewWriter(&seed)
	if err != nil {
		f.Fatal(err)
	}

	_, err = writer.Write(cpioFixture())
	if err != nil {
		f.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(seed.Bytes())
	f.Add([]byte("\xfd7zXZ\x00"))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > 64<<10 {
			return
		}

		reader, err := newRPMXZReader(bytes.NewReader(encoded), budget, budget)
		if err != nil {
			return
		}

		_, _ = io.CopyN(io.Discard, reader, budget)
	})
}

func TestInvalidCompressionFooter(t *testing.T) {
	payload := cpioFixture(cpioEntry{name: "file", mode: 0o100644, data: "hello"})
	archive := rpmFixture(t, "xz", payload)

	corrupted := bytes.Clone(archive)

	corrupted[len(corrupted)-1] ^= 1

	for name, invalid := range map[string][]byte{
		"truncated": archive[:len(archive)-1],
		"corrupted": corrupted,
	} {
		t.Run(name, func(t *testing.T) {
			err := extractFixture(t, invalid, t.TempDir())
			if err == nil {
				t.Fatal("damaged compressed payload accepted")
			}
		})
	}
}

func TestCPIOChecksum(t *testing.T) {
	payload := cpioFixture(cpioEntry{name: "file", mode: 0o100644, data: "hello"})
	copy(payload[:6], "070702")

	err := extractFixture(t, rpmFixture(t, "xz", payload), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}

	var sum uint32

	for _, b := range []byte("hello") {
		sum += uint32(b)
	}

	copy(payload[102:110], fmt.Sprintf("%08x", sum))

	err = extractFixture(t, rpmFixture(t, "xz", payload), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectSymlinkGraphEscape(t *testing.T) {
	cases := map[string][]cpioEntry{
		"symlink parent": {
			{name: "dir", mode: 0o120777, data: "."},
			{name: "dir/link", mode: 0o120777, data: ".."},
		},
		"symlink target": {
			{name: "alias", mode: 0o120777, data: "."},
			{name: "link", mode: 0o120777, data: "alias/../outside"},
		},
		"forward reference": {
			{name: "link", mode: 0o120777, data: "alias/../outside"},
			{name: "alias", mode: 0o120777, data: "."},
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			archive := rpmFixture(t, "xz", cpioFixture(entries...))

			err := extractFixture(t, archive, t.TempDir())
			if err == nil {
				t.Fatal("symlink graph escaping extraction root accepted")
			}
		})
	}
}

func TestRelativeAndDanglingSymlinks(t *testing.T) {
	payload := cpioFixture(
		cpioEntry{name: "dir", mode: 0o40755},
		cpioEntry{name: "bin/tool", mode: 0o100755, data: "tool"},
		cpioEntry{name: "dir/link", mode: 0o120777, data: "../bin/tool"},
		cpioEntry{name: "dangling", mode: 0o120777, data: "missing"},
	)
	destination := t.TempDir()

	err := extractFixture(t, rpmFixture(t, "xz", payload), destination)
	if err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(destination, "dir/link"))
	if err != nil || string(contents) != "tool" {
		t.Fatalf("relative symlink: contents %q, error %v", contents, err)
	}

	target, err := os.Readlink(filepath.Join(destination, "dangling"))
	if err != nil || target != "missing" {
		t.Fatalf("dangling symlink: target %q, error %v", target, err)
	}
}

func TestDirectoryPermissions(t *testing.T) {
	payload := cpioFixture(
		cpioEntry{name: "parent", mode: 0o40500},
		cpioEntry{name: "parent/child", mode: 0o40750},
		cpioEntry{name: "parent/child/file", mode: 0o100640, data: "contents"},
	)
	destination := t.TempDir()
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(destination, "parent"), 0o700)
	})

	err := extractFixture(t, rpmFixture(t, "xz", payload), destination)
	if err != nil {
		t.Fatal(err)
	}

	for name, mode := range map[string]os.FileMode{
		"parent": 0o500, "parent/child": 0o750, "parent/child/file": 0o640,
	} {
		info, err := os.Stat(filepath.Join(destination, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: expected mode %o, got %v, error %v", name, mode, info, err)
		}
	}
}

func TestRPMXZMultipleBlocks(t *testing.T) {
	const budget = 1 << 20

	var encoded bytes.Buffer

	writer, err := (xz.WriterConfig{DictCap: budget, BlockSize: 128}).NewWriter(&encoded)
	require.NoError(t, err)

	payload := bytes.Repeat([]byte("multiple blocks"), 30)

	_, err = writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	reader, err := newRPMXZReader(bytes.NewReader(encoded.Bytes()), budget, budget)
	require.NoError(t, err)

	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, payload, decoded)

	// Change the dictionary in a later block: checking only the first block
	// would allow the XZ decoder to allocate a 4 GiB dictionary here.
	damaged := bytes.Clone(encoded.Bytes())
	blockSize := (int(damaged[xz.HeaderLen]) + 1) * 4
	blockHeader := damaged[xz.HeaderLen : xz.HeaderLen+blockSize]
	next := bytes.Index(damaged[xz.HeaderLen+blockSize:], blockHeader)
	require.NotEqual(t, -1, next, "fixture must contain another block")

	next += xz.HeaderLen + blockSize

	block := damaged[next : next+blockSize]

	block[4] = 40
	binary.LittleEndian.PutUint32(block[len(block)-crc32.Size:],
		crc32.ChecksumIEEE(block[:len(block)-crc32.Size]))

	_, err = newRPMXZReader(bytes.NewReader(damaged), budget, budget)
	require.ErrorContains(t, err, "dictionary size exceeds max")
}

func TestRPMXZRejectConcatenatedStreams(t *testing.T) {
	archive := rpmFixture(t, "xz", cpioFixture())
	start := bytes.Index(archive, []byte("\xfd7zXZ\x00"))
	require.NotEqual(t, -1, start)

	stream := archive[start:]

	reader, err := newRPMXZReader(bytes.NewReader(bytes.Repeat(stream, 2)),
		maxRPMDecoderWindow, maxRPMPayloadSize)
	require.NoError(t, err)

	_, err = io.Copy(io.Discard, reader)
	require.ErrorContains(t, err, "unexpected data after stream")
}

func TestRPMXZDictionaryWithOptionalBlockSizes(t *testing.T) {
	// Block size, flags, compressed and uncompressed sizes, then LZMA2 filter.
	header := []byte{3, 0xc0}

	header = binary.AppendUvarint(header, 1000)
	header = binary.AppendUvarint(header, 2000)
	header = append(header, 0x21, 1, 40)
	header = append(header, make([]byte, 16-crc32.Size-len(header))...)
	header = binary.LittleEndian.AppendUint32(header, crc32.ChecksumIEEE(header))

	_, err := readRPMXZDict(bytes.NewReader(header[1:]), header[0], maxRPMDecoderWindow)
	require.ErrorContains(t, err, "dictionary size exceeds max")
}

func TestRPMXZPayloadBudget(t *testing.T) {
	payload := append(cpioFixture(), make([]byte, 1024)...)
	archive := rpmFixture(t, "xz", payload)
	start := bytes.Index(archive, []byte("\xfd7zXZ\x00"))
	require.NotEqual(t, -1, start)

	stream := archive[start:]

	for _, limit := range []int64{int64(len(payload)), int64(len(payload) - 1)} {
		reader, err := newRPMXZReader(bytes.NewReader(stream), maxRPMDecoderWindow, limit)
		if limit < int64(len(payload)) {
			require.ErrorContains(t, err, "RPM payload exceeds")

			continue
		}

		require.NoError(t, err)

		decoded, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.Equal(t, payload, decoded)
	}
}

func TestRPMXZBlockChecksum(t *testing.T) {
	archive := rpmFixture(t, "xz", cpioFixture())
	start := bytes.Index(archive, []byte("\xfd7zXZ\x00"))
	require.NotEqual(t, -1, start)

	stream := bytes.Clone(archive[start:])

	// A one-record index is eight bytes, followed by the twelve-byte footer.
	// The default writer's eight-byte CRC64 block check precedes the index.
	stream[len(stream)-12-8-1] ^= 1

	reader, err := newRPMXZReader(bytes.NewReader(stream), maxRPMDecoderWindow, maxRPMPayloadSize)
	require.NoError(t, err)

	_, err = io.Copy(io.Discard, reader)
	require.ErrorContains(t, err, "checksum")
}
