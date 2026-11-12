package pack

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	// zipEpochDate and zipEpochTime are the MS-DOS date and time of the zip
	// epoch, 1980-01-01 00:00: the year from 1980, the month and the day, each
	// from 1, in the bits 15-9, 8-5 and 4-0 of the date, and midnight. A zip
	// says no earlier time, and a date of 0, which says month 0, is none.
	zipEpochDate = 1<<5 | 1
	zipEpochTime = 0
	// extTimeExtraID is the ID of the extended timestamp extra field of
	// Info-ZIP, which holds a time in UTC (archive/zip writes it for
	// FileHeader.Modified).
	extTimeExtraID = 0x5455
	// extraHeaderLen is the length of the header of an extra field: its ID
	// and the length of its data, two bytes each.
	extraHeaderLen = 4
)

// stampZipEpoch dates the entry header the zip epoch, the time of every entry
// of the rocks tt packs, so the same files are the same bytes: in its MS-DOS
// date and time alone, which a reader shows as they are. Modified is left
// unset, which archive/zip would write as an extended timestamp too, and an
// extended timestamp the header has is dropped: it holds a time in UTC,
// which unzip shows in its local time zone - 1980-01-01 07:30 in Singapore.
func stampZipEpoch(header *zip.FileHeader) {
	header.Modified = time.Time{}
	// Modified, which replaces them, comes with the extended timestamp.
	header.ModifiedDate = zipEpochDate //nolint:staticcheck // See above.
	header.ModifiedTime = zipEpochTime //nolint:staticcheck // See above.
	header.Extra = withoutExtTime(header.Extra)
}

// withoutExtTime returns the extra fields extra without the extended
// timestamp. Fields it cannot read are kept as they are.
func withoutExtTime(extra []byte) []byte {
	var out []byte

	rest := extra

	for len(rest) >= extraHeaderLen {
		fieldID := binary.LittleEndian.Uint16(rest)
		size := int(binary.LittleEndian.Uint16(rest[2:]))

		if len(rest) < extraHeaderLen+size {
			break
		}

		if fieldID != extTimeExtraID {
			out = append(out, rest[:extraHeaderLen+size]...)
		}

		rest = rest[extraHeaderLen+size:]
	}

	return append(out, rest...)
}

// restampZip returns the zip archive at path with every entry dated the zip
// epoch (stampZipEpoch) and its data as it is, compressed: go-luarocks packs
// a rock with no time, a DOS date of 0, which unzip shows as 1980-00-00.
func restampZip(path string) ([]byte, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	defer func() { _ = archive.Close() }()

	var buf bytes.Buffer

	out := zip.NewWriter(&buf)

	err = out.SetComment(archive.Comment)
	if err != nil {
		return nil, fmt.Errorf("rewriting %s: %w", path, err)
	}

	for _, file := range archive.File {
		err = copyStamped(out, file)
		if err != nil {
			return nil, fmt.Errorf("rewriting %s: %s: %w", path, file.Name, err)
		}
	}

	err = out.Close()
	if err != nil {
		return nil, fmt.Errorf("rewriting %s: %w", path, err)
	}

	return buf.Bytes(), nil
}

// copyStamped copies the entry file into out, dated the zip epoch.
func copyStamped(out *zip.Writer, file *zip.File) error {
	header := file.FileHeader

	stampZipEpoch(&header)

	data, err := file.OpenRaw()
	if err != nil {
		return err //nolint:wrapcheck // restampZip names the entry.
	}

	writer, err := out.CreateRaw(&header)
	if err != nil {
		return err //nolint:wrapcheck // restampZip names the entry.
	}

	_, err = io.Copy(writer, data)

	return err //nolint:wrapcheck // restampZip names the entry.
}
