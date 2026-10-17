package install

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"hash/crc64"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

const (
	// These are extraction limits, not requirements of the RPM/CPIO formats.
	// Tarantool runtime and devel packages fit well within these bounds.
	maxRPMHeaderSize    = 64 << 20
	maxRPMPayloadSize   = 1 << 30
	maxRPMDecoderWindow = 64 << 20
	maxRPMXZBlocks      = 100_000
	xzAlignment         = 4
	maxCPIOEntries      = 100_000
	maxCPIOPathSize     = 1 << 20
	cpioWorkingDirMode  = 0o755
	cpioNewFileMode     = 0o600

	// RPM v4: the lead identifies the archive; signature headers are padded
	// to eight bytes. The main header and its index use big-endian integers.
	// https://rpm-software-management.github.io/rpm/manual/format_v4.html
	rpmLeadMagic             = "\xed\xab\xee\xdb"
	rpmLeadMajorVersion      = 3
	rpmHeaderMagic           = "\x8e\xad\xe8"
	rpmHeaderVersion         = 1
	rpmSignatureAlignment    = 8
	rpmIndexEntrySize        = 16 // Four 32-bit fields: tag, type, offset, count.
	rpmPayloadFormatTag      = 1124
	rpmPayloadCompressionTag = 1125
	rpmStringType            = 6 // A NUL-terminated string with count == 1.

	// newc/CRC: six magic bytes followed by thirteen eight-digit hex fields.
	// Both the filename and file data are padded to four-byte boundaries.
	// https://www.kernel.org/doc/html/latest/driver-api/early-userspace/buffer-format.html
	cpioMagicNewc    = "070701"
	cpioMagicCRC     = "070702"
	cpioHexFieldSize = 8
	cpioHexBase      = 16
	cpioFieldBits    = 32
	cpioHeaderSize   = len(cpioMagicNewc) + 13*cpioHexFieldSize
	cpioAlignment    = 4
	cpioTrailer      = "TRAILER!!!"

	// The CPIO mode field uses the Unix file type and permission bits.
	cpioFileTypeMask    = 0o170000
	cpioDirectory       = 0o040000
	cpioRegularFile     = 0o100000
	cpioSymlink         = 0o120000
	cpioPermissionsMask = 0o777
)

// The lead is always 96 bytes. Fields other than magic/version are historical.
// https://rpm-software-management.github.io/rpm/manual/format_lead.html
type rpmLead struct {
	Magic         [4]byte
	Major         uint8
	Minor         uint8
	PackageType   uint16
	Architecture  uint16
	Name          [66]byte
	OS            uint16
	SignatureType uint16
	Reserved      [16]byte
}

// https://rpm-software-management.github.io/rpm/manual/format_header.html
type rpmHeader struct {
	Magic      [3]byte
	Version    uint8
	Reserved   [4]byte
	IndexCount uint32
	DataSize   uint32
}

type rpmIndexEntry struct {
	Tag    uint32
	Type   uint32
	Offset int32
	Count  uint32
}

var errInvalidRPM = errors.New("invalid RPM archive")

// extractRPM unpacks an RPM's CPIO payload into destination, creating it if needed.
// It preserves file permissions, relative symlinks and hardlinks. Absolute paths,
// links outside destination and special files are rejected. On failure, callers
// must remove any partially extracted files; use a temporary destination.
func extractRPM(archivePath, destination string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("extract RPM data: %w", err)
	}

	defer func() { _ = archive.Close() }()

	var lead rpmLead

	readLeadErr := binary.Read(archive, binary.BigEndian, &lead)
	if readLeadErr != nil {
		return fmt.Errorf("read RPM lead: %w", readLeadErr)
	}

	if string(lead.Magic[:]) != rpmLeadMagic || lead.Major != rpmLeadMajorVersion {
		return fmt.Errorf("%w: invalid RPM lead", errInvalidRPM)
	}

	_, readSignatureErr := readRPMHeader(archive)
	if readSignatureErr != nil {
		return fmt.Errorf("read RPM signature: %w", readSignatureErr)
	}

	position, err := archive.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("extract RPM data: %w", err)
	}

	err = discardArchivePadding(archive, position, rpmSignatureAlignment)
	if err != nil {
		return fmt.Errorf("read RPM signature padding: %w", err)
	}

	tags, err := readRPMHeader(archive)
	if err != nil {
		return fmt.Errorf("read RPM header: %w", err)
	}

	if tags[rpmPayloadFormatTag] != "cpio" {
		return fmt.Errorf("%w: unsupported RPM payload format %q",
			errInvalidRPM, tags[rpmPayloadFormatTag])
	}

	if tags[rpmPayloadCompressionTag] != "xz" {
		return fmt.Errorf("%w: unsupported RPM payload compression %q",
			errInvalidRPM, tags[rpmPayloadCompressionTag])
	}

	payload, err := newRPMXZReader(archive, maxRPMDecoderWindow, maxRPMPayloadSize)
	if err != nil {
		return fmt.Errorf("open RPM payload: %w", err)
	}

	err = os.MkdirAll(destination, cpioWorkingDirMode)
	if err != nil {
		return fmt.Errorf("extract RPM data: %w", err)
	}

	root, err := os.OpenRoot(destination)
	if err != nil {
		return fmt.Errorf("extract RPM data: %w", err)
	}

	defer func() { _ = root.Close() }()

	// Include the footer/padding in the budget, so data after TRAILER!!! cannot
	// bypass the limit. The extra byte distinguishes an exact fit from overflow.
	boundedPayload := &io.LimitedReader{R: payload, N: maxRPMPayloadSize + 1}

	err = extractCPIO(boundedPayload, root)

	if boundedPayload.N == 0 {
		return fmt.Errorf("%w: RPM payload exceeds %d bytes", errInvalidRPM, maxRPMPayloadSize)
	}

	if err != nil {
		return fmt.Errorf("extract RPM payload: %w", err)
	}

	// Read through the compression footer to detect truncation and checksum errors.
	_, readFooterErr := io.Copy(io.Discard, boundedPayload)
	if boundedPayload.N == 0 {
		return fmt.Errorf("%w: RPM payload exceeds %d bytes", errInvalidRPM, maxRPMPayloadSize)
	}

	if readFooterErr != nil {
		return fmt.Errorf("read RPM payload footer: %w", readFooterErr)
	}

	return nil
}

func readRPMHeader(reader io.Reader) (map[uint32]string, error) {
	var header rpmHeader

	readHeaderErr := binary.Read(reader, binary.BigEndian, &header)
	if readHeaderErr != nil {
		return nil, fmt.Errorf("read RPM header: %w", readHeaderErr)
	}

	if string(header.Magic[:]) != rpmHeaderMagic || header.Version != rpmHeaderVersion ||
		header.Reserved != [4]byte{} {
		return nil, fmt.Errorf("%w: invalid header magic or version", errInvalidRPM)
	}

	indexSize := uint64(header.IndexCount) * rpmIndexEntrySize
	totalSize := indexSize + uint64(header.DataSize)

	if totalSize > maxRPMHeaderSize {
		return nil, fmt.Errorf("%w: RPM header exceeds %d bytes", errInvalidRPM, maxRPMHeaderSize)
	}

	// Grow only as bytes arrive: a tiny truncated input must not trigger a large
	// allocation just by declaring a large header.
	encoded, err := io.ReadAll(io.LimitReader(reader, int64(totalSize)))
	if err != nil {
		return nil, fmt.Errorf("read RPM header: %w", err)
	}

	if uint64(len(encoded)) != totalSize {
		return nil, fmt.Errorf("read RPM header: %w", io.ErrUnexpectedEOF)
	}

	index, data := encoded[:indexSize], encoded[indexSize:]
	tags := make(map[uint32]string)

	for len(index) != 0 {
		var entry rpmIndexEntry

		_, err := binary.Decode(index, binary.BigEndian, &entry)
		if err != nil {
			return nil, fmt.Errorf("read RPM index: %w", err)
		}

		index = index[rpmIndexEntrySize:]

		if entry.Offset < 0 || uint32(entry.Offset) > header.DataSize {
			return nil, fmt.Errorf("%w: tag %d points outside header data",
				errInvalidRPM, entry.Tag)
		}

		// Only payload tags affect extraction. Do not walk unused string arrays:
		// overlapping tags could otherwise make validation quadratic in header size.
		if entry.Tag != rpmPayloadFormatTag && entry.Tag != rpmPayloadCompressionTag {
			continue
		}

		if entry.Type != rpmStringType || entry.Count != 1 {
			return nil, fmt.Errorf("%w: invalid payload tag %d", errInvalidRPM, entry.Tag)
		}

		if _, exists := tags[entry.Tag]; exists {
			return nil, fmt.Errorf("%w: duplicate payload tag %d", errInvalidRPM, entry.Tag)
		}

		value, _, terminated := bytes.Cut(data[entry.Offset:], []byte{0})
		if !terminated {
			return nil, fmt.Errorf("%w: unterminated string for tag %d", errInvalidRPM, entry.Tag)
		}

		tags[entry.Tag] = string(value)
	}

	return tags, nil
}

type cpioHeader struct {
	inode       uint32
	mode        uint32
	linkCount   uint32
	fileSize    uint32
	deviceMajor uint32
	deviceMinor uint32
	nameSize    uint32
	checksum    uint32
	hasChecksum bool
}

type hardlinkKey struct {
	deviceMajor uint32
	deviceMinor uint32
	inode       uint32
}

type hardlinkGroup struct {
	names  []string
	source string
}

// readCPIOHeader decodes the newc header; ownership, timestamps and device targets
// are validated as numbers but are not restored during extraction.
func readCPIOHeader(reader io.Reader) (cpioHeader, error) {
	var header cpioHeader

	var encoded [cpioHeaderSize]byte

	_, err := io.ReadFull(reader, encoded[:])
	if err != nil {
		return header, fmt.Errorf("read CPIO header: %w", err)
	}

	magic := string(encoded[:len(cpioMagicNewc)])
	if magic != cpioMagicNewc && magic != cpioMagicCRC {
		return header, fmt.Errorf("%w: unsupported CPIO magic %q", errInvalidRPM, magic)
	}

	header.hasChecksum = magic == cpioMagicCRC

	fields := [...]*uint32{
		&header.inode,
		&header.mode,
		nil, // uid.
		nil, // gid.
		&header.linkCount,
		nil, // mtime.
		&header.fileSize,
		&header.deviceMajor,
		&header.deviceMinor,
		nil, // rdevmajor.
		nil, // rdevminor.
		&header.nameSize,
		&header.checksum,
	}

	for index, field := range fields {
		start := len(cpioMagicNewc) + index*cpioHexFieldSize

		value, err := strconv.ParseUint(
			string(encoded[start:start+cpioHexFieldSize]), cpioHexBase, cpioFieldBits)
		if err != nil {
			return header, fmt.Errorf("invalid CPIO header: %w", err)
		}

		if field != nil {
			*field = uint32(value)
		}
	}

	return header, nil
}

func extractCPIO(reader io.Reader, root *os.Root) error {
	links := make(map[hardlinkKey]*hardlinkGroup)
	directories := make(map[string]os.FileMode)

	var symlinks []string

	for entries := 0; ; entries++ {
		header, err := readCPIOHeader(reader)
		if err != nil {
			return err
		}

		size, nameSize := int64(header.fileSize), int64(header.nameSize)
		if nameSize < 1 || nameSize > maxCPIOPathSize {
			return fmt.Errorf("%w: invalid CPIO filename size %d", errInvalidRPM, nameSize)
		}

		nameBytes := make([]byte, int(nameSize))

		_, readNameErr := io.ReadFull(reader, nameBytes)
		if readNameErr != nil {
			return fmt.Errorf("extract RPM data: %w", readNameErr)
		}

		if nameBytes[len(nameBytes)-1] != 0 ||
			bytes.IndexByte(nameBytes[:len(nameBytes)-1], 0) >= 0 {
			return fmt.Errorf("%w: invalid CPIO filename", errInvalidRPM)
		}

		err = discardArchivePadding(reader, int64(cpioHeaderSize)+nameSize, cpioAlignment)
		if err != nil {
			return fmt.Errorf("extract RPM data: %w", err)
		}

		name := string(nameBytes[:len(nameBytes)-1])
		if name == cpioTrailer {
			if size != 0 {
				return fmt.Errorf("%w: invalid CPIO trailer", errInvalidRPM)
			}

			break
		}

		if entries >= maxCPIOEntries {
			return fmt.Errorf("%w: CPIO payload exceeds %d entries", errInvalidRPM, maxCPIOEntries)
		}

		if size > maxRPMPayloadSize {
			return fmt.Errorf("%w: CPIO file %q exceeds payload size limit", errInvalidRPM, name)
		}

		name = path.Clean(name)
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
			return fmt.Errorf("%w: unsafe CPIO path %q", errInvalidRPM, name)
		}

		err = root.MkdirAll(filepath.Dir(name), cpioWorkingDirMode)
		if err != nil {
			return fmt.Errorf("extract RPM data: %w", err)
		}

		var checksum cpioByteSum

		content := io.TeeReader(io.LimitReader(reader, size), &checksum)

		if header.mode&cpioFileTypeMask == cpioSymlink {
			symlinks = append(symlinks, name)
		}

		err = extractCPIOEntry(content, root, name, header, links, directories)
		if err != nil {
			return err
		}

		if header.hasChecksum && uint32(checksum) != header.checksum {
			return fmt.Errorf("%w: CPIO checksum mismatch for %q", errInvalidRPM, name)
		}

		err = discardArchivePadding(reader, size, cpioAlignment)
		if err != nil {
			return fmt.Errorf("extract RPM data: %w", err)
		}
	}

	return finalizeCPIO(root, symlinks, links, directories)
}

func finalizeCPIO(root *os.Root, symlinks []string, links map[hardlinkKey]*hardlinkGroup,
	directories map[string]os.FileMode,
) error {
	for _, name := range symlinks {
		_, err := root.Stat(name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("validate CPIO symlink %q: %w", name, err)
		}
	}

	for _, group := range links {
		if group.source == "" {
			group.source = group.names[0]
		}

		for _, name := range group.names {
			if name == group.source {
				continue
			}

			err := root.Remove(name)
			if err != nil {
				return fmt.Errorf("extract RPM data: %w", err)
			}

			err = root.Link(group.source, name)
			if err != nil {
				return fmt.Errorf("extract RPM data: %w", err)
			}
		}
	}

	// Apply directory permissions last, after all children and hardlinks exist.
	names := make([]string, 0, len(directories))
	for name := range directories {
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		return strings.Count(names[i], "/") > strings.Count(names[j], "/")
	})

	for _, name := range names {
		err := root.Chmod(name, directories[name])
		if err != nil {
			return fmt.Errorf("extract RPM data: %w", err)
		}
	}

	return nil
}

func extractCPIOEntry(content io.Reader, root *os.Root, name string, header cpioHeader,
	links map[hardlinkKey]*hardlinkGroup, directories map[string]os.FileMode,
) error {
	mode, size := header.mode, int64(header.fileSize)
	switch mode & cpioFileTypeMask {
	case cpioDirectory:
		if size != 0 {
			return fmt.Errorf("%w: directory %q has contents", errInvalidRPM, name)
		}

		err := root.MkdirAll(name, cpioWorkingDirMode)
		if err != nil {
			return fmt.Errorf("extract RPM data: %w", err)
		}

		directories[name] = os.FileMode(mode & cpioPermissionsMask)
	case cpioRegularFile:
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, cpioNewFileMode)
		if err != nil {
			return fmt.Errorf("extract RPM data: %w", err)
		}

		_, copyErr := io.CopyN(file, content, size)
		chmodErr := file.Chmod(os.FileMode(mode & cpioPermissionsMask))
		closeErr := file.Close()

		if copyErr != nil {
			return fmt.Errorf("write CPIO file: %w", copyErr)
		}

		if chmodErr != nil {
			return fmt.Errorf("write CPIO file: %w", chmodErr)
		}

		if closeErr != nil {
			return fmt.Errorf("write CPIO file: %w", closeErr)
		}

		if header.linkCount > 1 {
			key := hardlinkKey{
				deviceMajor: header.deviceMajor,
				deviceMinor: header.deviceMinor,
				inode:       header.inode,
			}
			group := links[key]

			if group == nil {
				group = &hardlinkGroup{names: nil, source: ""}
				links[key] = group
			}

			group.names = append(group.names, name)
			if size > 0 {
				if group.source != "" {
					return fmt.Errorf("%w: multiple contents for CPIO hardlink %q",
						errInvalidRPM, name)
				}

				group.source = name
			}
		}
	case cpioSymlink:
		if size < 1 || size > maxCPIOPathSize {
			return fmt.Errorf("%w: invalid symlink size for %q", errInvalidRPM, name)
		}

		target := make([]byte, int(size))

		_, readTargetErr := io.ReadFull(content, target)
		if readTargetErr != nil {
			return fmt.Errorf("extract RPM data: %w", readTargetErr)
		}

		if path.IsAbs(string(target)) || bytes.ContainsAny(target, "\x00\\") ||
			!filepath.IsLocal(path.Join(path.Dir(name), string(target))) {
			return fmt.Errorf("%w: unsafe symlink target %q", errInvalidRPM, target)
		}

		err := root.Symlink(string(target), name)
		if err != nil {
			return fmt.Errorf("extract RPM data: %w", err)
		}
	default:
		return fmt.Errorf("%w: unsupported CPIO file mode %o for %q", errInvalidRPM, mode, name)
	}

	return nil
}

type cpioByteSum uint32

func (s *cpioByteSum) Write(data []byte) (int, error) {
	for _, b := range data {
		*s += cpioByteSum(b)
	}

	return len(data), nil
}

// discardArchivePadding advances to the next boundary after size bytes.
// The modulo leaves already aligned fields untouched.
func discardArchivePadding(reader io.Reader, size, alignment int64) error {
	padding := (alignment - size%alignment) % alignment

	_, err := io.CopyN(io.Discard, reader, padding)
	if err != nil {
		return fmt.Errorf("read archive padding: %w", err)
	}

	return nil
}

type rpmXZBlockReader struct {
	source io.Reader
	count  int64
}

func (r *rpmXZBlockReader) Read(data []byte) (int, error) {
	count, err := r.source.Read(data)

	r.count += int64(count)

	return count, err //nolint:wrapcheck // Preserve the underlying reader's EOF.
}

// newRPMXZReader reads the single XZ stream used by Tarantool RPMs. xz.DictCap
// is a minimum, not a memory limit: each block can request a larger dictionary.
// Check every block using a bounded LZMA2 decoder before opening the XZ reader,
// which then verifies all block checksums, the index and the footer. Checking
// with the actual decoder also handles malformed LZMA2 chunk boundaries safely.
func newRPMXZReader(source io.ReadSeeker, dictMax int, payloadMax int64) (io.Reader, error) {
	start, err := source.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("locate XZ stream: %w", err)
	}

	err = checkRPMXZ(bufio.NewReader(source), dictMax, payloadMax)
	if err != nil {
		return nil, err
	}

	_, err = source.Seek(start, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("rewind XZ stream: %w", err)
	}

	reader, err := (xz.ReaderConfig{SingleStream: true}).NewReader(bufio.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("read XZ stream: %w", err)
	}

	return reader, nil
}

func checkRPMXZ(source io.Reader, dictMax int, payloadMax int64) error {
	var header [xz.HeaderLen]byte

	_, err := io.ReadFull(source, header[:])
	if err != nil {
		return fmt.Errorf("read XZ header: %w", err)
	}

	if !xz.ValidHeader(header[:]) {
		return fmt.Errorf("%w: invalid XZ header", errInvalidRPM)
	}

	// The stream header's check ID is validated by ValidHeader above.
	checkSize := map[byte]int64{
		xz.None: 0, xz.CRC32: crc32.Size, xz.CRC64: crc64.Size, xz.SHA256: sha256.Size,
	}[header[7]]
	remaining := payloadMax + 1

	for blocks := 0; ; blocks++ {
		var size [1]byte

		_, err = io.ReadFull(source, size[:])
		if err != nil {
			return fmt.Errorf("read XZ block: %w", err)
		}

		if size[0] == 0 { // Index; the XZ reader verifies it without opening more blocks.
			return nil
		}

		if blocks >= maxRPMXZBlocks {
			return fmt.Errorf("%w: too many XZ blocks", errInvalidRPM)
		}

		dictCap, err := readRPMXZDict(source, size[0], dictMax)
		if err != nil {
			return err
		}

		compressed := &rpmXZBlockReader{source: source, count: 0}

		block, err := (lzma.Reader2Config{DictCap: dictCap}).NewReader2(compressed)
		if err != nil {
			return fmt.Errorf("read LZMA2 block: %w", err)
		}

		count, err := io.Copy(io.Discard, io.LimitReader(block, remaining))
		if count == remaining {
			return fmt.Errorf("%w: RPM payload exceeds %d bytes", errInvalidRPM, payloadMax)
		}

		if err != nil {
			return fmt.Errorf("read LZMA2 block: %w", err)
		}

		if !block.EOS() {
			return fmt.Errorf("%w: missing LZMA2 end marker", errInvalidRPM)
		}

		remaining -= count

		err = discardArchivePadding(source, compressed.count, xzAlignment)
		if err != nil {
			return err
		}

		_, err = io.CopyN(io.Discard, source, checkSize)
		if err != nil {
			return fmt.Errorf("read XZ block check: %w", err)
		}
	}
}

// XZ block headers are at most 1024 bytes. Tarantool RPMs use one LZMA2 filter.
// https://tukaani.org/xz/xz-file-format.txt, sections 3.1 and 5.4.
func readRPMXZDict(source io.Reader, size byte, dictMax int) (int, error) {
	header := make([]byte, (int(size)+1)*xzAlignment)

	header[0] = size

	_, err := io.ReadFull(source, header[1:])
	if err != nil {
		return 0, fmt.Errorf("read XZ block header: %w", err)
	}

	data := header[:len(header)-crc32.Size]
	if binary.LittleEndian.Uint32(header[len(data):]) != crc32.ChecksumIEEE(data) {
		return 0, fmt.Errorf("%w: invalid XZ block header checksum", errInvalidRPM)
	}

	// Reserved flags and filter chains are unsupported by the XZ decoder.
	if data[1]&0x3f != 0 {
		return 0, fmt.Errorf("%w: unsupported XZ block flags", errInvalidRPM)
	}

	fields := bytes.NewReader(data[2:])
	for _, flag := range []byte{0x40, 0x80} { // Optional compressed/uncompressed sizes.
		if data[1]&flag != 0 {
			_, err = binary.ReadUvarint(fields)
			if err != nil {
				return 0, fmt.Errorf("read XZ block size: %w", err)
			}
		}
	}

	var filter [3]byte

	_, err = io.ReadFull(fields, filter[:])
	if err != nil {
		return 0, fmt.Errorf("read XZ filter: %w", err)
	}

	if filter[0] != 0x21 || filter[1] != 1 {
		return 0, fmt.Errorf("%w: unsupported XZ filter", errInvalidRPM)
	}

	dictCap, err := lzma.DecodeDictCap(filter[2])
	if err != nil {
		return 0, fmt.Errorf("read XZ dictionary size: %w", err)
	}

	if dictCap > int64(dictMax) {
		return 0, fmt.Errorf("%w: XZ dictionary size exceeds max %d", errInvalidRPM, dictMax)
	}

	return int(dictCap), nil
}
