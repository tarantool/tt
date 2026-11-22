package rws

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/tarantool/tt/v3/internal/untar"
)

const (
	// maxDebDataSize bounds the unpacked data of a package, an extraction
	// limit rather than one of the format: the executable of Tarantool 3.9
	// with its debug information is 110 MB.
	maxDebDataSize = 1 << 30
	// maxDebMembers bounds the members of the ar archive looked through for
	// the data: a package has three.
	maxDebMembers = 16
	// maxDebFormatSize bounds the debian-binary member, "2.0\n".
	maxDebFormatSize = 64

	// A deb is an ar archive: a magic string, then each member as a header
	// of 60 bytes - the name, padded with spaces, the size in decimal at 48
	// and a terminator - and the content, padded to an even size. Its first
	// member is debian-binary, the version of the format; the data is the
	// tar of data.tar.<compression>. See deb(5) and ar(5).
	arMagic        = "!<arch>\n"
	arHeaderSize   = 60
	arNameSize     = 16
	arSizeOffset   = 48
	arSizeLength   = 10
	arTerminator   = "`\n"
	arAlignment    = 2
	debFormat      = "debian-binary"
	debFormatMajor = "2"
	debDataPrefix  = "data.tar"
	// debData is the only data member the packages of the repository have.
	debData = "data.tar.gz"
)

var errInvalidDeb = errors.New("invalid deb archive")

// extractDeb unpacks the data of the deb at archivePath into destination,
// creating it if needed, through untar: with the permissions of the files
// and the relative links that stay inside it. dataLimit bounds the data
// unpacked. On failure, callers must remove any partially extracted files;
// use a temporary destination.
func extractDeb(archivePath, destination string, dataLimit int64) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open deb archive: %w", err)
	}

	defer func() { _ = archive.Close() }()

	info, err := archive.Stat()
	if err != nil {
		return fmt.Errorf("open deb archive: %w", err)
	}

	data, err := debDataMember(archive, info.Size())
	if err != nil {
		return err
	}

	stream, err := gzip.NewReader(data)
	if err != nil {
		return fmt.Errorf("%w: decompress %s: %w", errInvalidDeb, debData, err)
	}

	// The extra byte tells an exact fit from data over the limit.
	bounded := &io.LimitedReader{R: stream, N: dataLimit + 1}

	err = untar.ExtractTar(bounded, destination, untar.Options{Symlinks: true, EntryModes: true})
	if bounded.N == 0 {
		return fmt.Errorf("%w: deb data exceeds %d bytes", errInvalidDeb, dataLimit)
	}

	if err != nil {
		return fmt.Errorf("extract deb data: %w", err)
	}

	// Read through the padding and the gzip footer, which detects truncation
	// and checksum errors.
	_, err = io.Copy(io.Discard, bounded)
	if bounded.N == 0 {
		return fmt.Errorf("%w: deb data exceeds %d bytes", errInvalidDeb, dataLimit)
	}

	if err != nil {
		return fmt.Errorf("%w: read %s: %w", errInvalidDeb, debData, err)
	}

	return nil
}

// debDataMember returns the content of the data member of the deb archive
// of size bytes. The archive must begin with a debian-binary of format 2;
// a data member compressed other than with gzip is unsupported.
func debDataMember(archive io.ReaderAt, size int64) (*io.SectionReader, error) {
	magic := make([]byte, len(arMagic))

	err := readFullAt(archive, magic, 0)
	if err != nil || string(magic) != arMagic {
		return nil, fmt.Errorf("%w: no ar archive", errInvalidDeb)
	}

	offset := int64(len(arMagic))

	for members := 0; ; members++ {
		if members >= maxDebMembers {
			return nil, fmt.Errorf("%w: no %s among the first %d members",
				errInvalidDeb, debData, maxDebMembers)
		}

		name, content, err := readArMember(archive, offset, size)
		if err != nil {
			return nil, err
		}

		switch {
		case members == 0:
			err = checkDebFormat(name, content)
			if err != nil {
				return nil, err
			}
		case name == debData:
			return content, nil
		case strings.HasPrefix(name, debDataPrefix):
			return nil, fmt.Errorf("%w: unsupported deb data compression %q, only %s is",
				errInvalidDeb, name, debData)
		}

		offset += arHeaderSize + content.Size() + content.Size()%arAlignment
	}
}

// readArMember reads the header of the ar member at offset of the archive
// of size bytes and returns the name and the content of the member.
func readArMember(archive io.ReaderAt, offset, size int64) (string, *io.SectionReader, error) {
	if offset >= size {
		return "", nil, fmt.Errorf("%w: no %s", errInvalidDeb, debData)
	}

	header := make([]byte, arHeaderSize)

	err := readFullAt(archive, header, offset)
	if err != nil {
		return "", nil, fmt.Errorf("%w: read the member header at %d: %w",
			errInvalidDeb, offset, err)
	}

	if string(header[arHeaderSize-len(arTerminator):]) != arTerminator {
		return "", nil, fmt.Errorf("%w: invalid member header at %d", errInvalidDeb, offset)
	}

	// GNU ar ends a name with a slash.
	name := strings.TrimSuffix(strings.TrimRight(string(header[:arNameSize]), " "), "/")

	length, err := strconv.ParseInt(strings.TrimRight(
		string(header[arSizeOffset:arSizeOffset+arSizeLength]), " "), 10, 64)
	if err != nil || length < 0 || length > size-offset-arHeaderSize {
		return "", nil, fmt.Errorf("%w: invalid size of the member %q", errInvalidDeb, name)
	}

	return name, io.NewSectionReader(archive, offset+arHeaderSize, length), nil
}

// readFullAt fills buffer from archive at offset; a short read is
// io.ErrUnexpectedEOF. A full read may end with io.EOF at the end of the
// archive, which is no error.
func readFullAt(archive io.ReaderAt, buffer []byte, offset int64) error {
	read, err := archive.ReadAt(buffer, offset)
	if read == len(buffer) {
		return nil
	}

	if err == nil || errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}

	return fmt.Errorf("read deb archive: %w", err)
}

// checkDebFormat checks that the first member of a deb is debian-binary of
// format 2.x.
func checkDebFormat(name string, content *io.SectionReader) error {
	if name != debFormat {
		return fmt.Errorf("%w: the first member is %q, not %s", errInvalidDeb, name, debFormat)
	}

	if content.Size() > maxDebFormatSize {
		return fmt.Errorf("%w: %s exceeds %d bytes", errInvalidDeb, debFormat, maxDebFormatSize)
	}

	format, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", errInvalidDeb, debFormat, err)
	}

	major, _, _ := strings.Cut(strings.TrimSpace(string(format)), ".")
	if major != debFormatMajor {
		return fmt.Errorf("%w: unsupported deb format %q", errInvalidDeb,
			strings.TrimSpace(string(format)))
	}

	return nil
}
