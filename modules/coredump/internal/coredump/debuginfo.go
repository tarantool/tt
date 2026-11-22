package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

// ntGNUBuildID is the type of the GNU note that holds the build ID.
const ntGNUBuildID = 3

// noteHeaderSize is the size of the header of an ELF note: the sizes of its
// name and its descriptor, and its type.
const noteHeaderSize = 12

// debugFileDir is where, under the root of an archive, pack puts the
// separate debugging information of the tarantool, as debuginfo packages
// install it under /usr/lib/debug.
var debugFileDir = filepath.Join("usr", "lib", "debug")

// errNoDebugInfo reports a tarantool gdb finds no debugging information
// for.
var errNoDebugInfo = errors.New("no debugging information")

// hasDWARF reports whether file carries debugging information: a
// .debug_info section, or a compressed .zdebug_info one, with content. A
// section of no bits, as a stripped file may keep, is none.
func hasDWARF(file *elf.File) bool {
	for _, name := range []string{".debug_info", ".zdebug_info"} {
		section := file.Section(name)
		if section != nil && section.Type != elf.SHT_NOBITS && section.Size > 0 {
			return true
		}
	}

	return false
}

// buildID returns the GNU build ID of file in hex: the note of the
// .note.gnu.build-id section, or of a note segment when the sections are
// gone. It is empty when file has none.
func buildID(file *elf.File) string {
	section := file.Section(".note.gnu.build-id")
	if section != nil && section.Type == elf.SHT_NOTE {
		data, err := section.Data()
		if err == nil {
			id := noteBuildID(data, file.ByteOrder, section.Addralign)
			if id != "" {
				return id
			}
		}
	}

	for _, prog := range file.Progs {
		if prog.Type != elf.PT_NOTE {
			continue
		}

		data, err := io.ReadAll(prog.Open())
		if err != nil {
			continue
		}

		id := noteBuildID(data, file.ByteOrder, prog.Align)
		if id != "" {
			return id
		}
	}

	return ""
}

// noteBuildID returns the GNU build ID among the notes of data, in hex, or
// "" when there is none. The name and the descriptor of a note are padded
// to align, 4 bytes unless the section or the segment says 8.
func noteBuildID(data []byte, order binary.ByteOrder, align uint64) string {
	pad := uint64(4) //nolint:mnd // The alignment of the notes of ELF.
	if align == 8 {  //nolint:mnd // A segment of 8-byte aligned notes.
		pad = align
	}

	padded := func(size uint64) uint64 {
		return (size + pad - 1) / pad * pad
	}

	for uint64(len(data)) >= noteHeaderSize {
		nameSize := uint64(order.Uint32(data[0:4]))
		descSize := uint64(order.Uint32(data[4:8]))
		noteType := order.Uint32(data[8:12])

		nameEnd := noteHeaderSize + padded(nameSize)
		descEnd := nameEnd + padded(descSize)

		if descEnd > uint64(len(data)) || nameEnd+descSize > uint64(len(data)) {
			return ""
		}

		name := data[noteHeaderSize : noteHeaderSize+nameSize]
		if noteType == ntGNUBuildID && bytes.Equal(name, []byte("GNU\x00")) && descSize > 0 {
			return hex.EncodeToString(data[nameEnd : nameEnd+descSize])
		}

		data = data[descEnd:]
	}

	return ""
}

// debugFilePath returns the path of the separate debugging information of
// the build ID id under root: .build-id/<first two digits>/<the rest>.debug,
// where gdb looks for it.
func debugFilePath(root, id string) string {
	return filepath.Join(root, ".build-id", id[:2], id[2:]+".debug")
}

// checkDebugInfo returns nil when gdb finds debugging information for the
// tarantool of the artefacts in dir: of its own, or in the debug file of
// its build ID under usr/lib/debug of the artefacts, which gdb.sh points
// gdb to. Otherwise the error wraps errNoDebugInfo and says what was looked
// for; a tarantool that is no ELF file is another error.
func checkDebugInfo(dir string) error {
	tarantool := filepath.Join(dir, "tarantool")

	file, err := elf.Open(tarantool)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", tarantool, err)
	}

	defer func() { _ = file.Close() }()

	if hasDWARF(file) {
		return nil
	}

	build := buildID(file)
	if len(build) < 3 { //nolint:mnd // The directory is named by two digits, the file by the rest.
		return fmt.Errorf("%w: no .debug_info in the tarantool, and no build ID to find a "+
			"debug file by", errNoDebugInfo)
	}

	debugFile := debugFilePath(filepath.Join(dir, debugFileDir), build)

	debug, err := elf.Open(debugFile)
	if err == nil {
		defer func() { _ = debug.Close() }()

		if hasDWARF(debug) {
			return nil
		}
	}

	rel, err := filepath.Rel(dir, debugFile)
	if err != nil {
		rel = debugFile
	}

	return fmt.Errorf("%w: no .debug_info in the tarantool, and no %s of its build ID in "+
		"the archive", errNoDebugInfo, rel)
}
