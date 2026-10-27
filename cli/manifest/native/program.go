package native

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// ErrNotNative is wrapped by InspectProgram's error for a file that is
// neither ELF nor Mach-O.
var ErrNotNative = errors.New("not an ELF or Mach-O file")

// cStringChunk is how much of a string table InspectProgram reads at a time
// while it looks for the end of a library name.
const cStringChunk = 256

// Program describes a native program: the platforms it runs on, whether a
// process can be started from it, and, for an ELF one, what the system has to
// supply when it is loaded.
//
// Unlike Files and FilePlatforms, which only read the header, InspectProgram
// reads the program headers and the dynamic segment too. That is fine for the
// few files a build declares its output, not for every file of an archive.
type Program struct {
	// Platforms lists the platforms the program runs on, as FilePlatforms
	// reports them.
	Platforms []Platform
	// Executable reports that a process can be started from the file: an ELF
	// executable or position-independent executable, or a Mach-O executable
	// in every slice. A shared library, an object file or a core dump is
	// native, but not a program.
	Executable bool
	// Interpreter is the dynamic loader an ELF program names (PT_INTERP);
	// empty when it names none.
	Interpreter string
	// Needed lists the shared libraries the program asks the loader for
	// (DT_NEEDED of an ELF program, the dylibs of a Mach-O one).
	Needed []string
}

// Static reports whether the program needs nothing from the system it runs
// on to be loaded: no loader and no shared library.
//
// A static-pie program, which Rust builds for a musl target, has a dynamic
// segment of its own - it relocates itself - so the dynamic segment is not
// what tells a dynamic program; the loader and the libraries are.
func (p Program) Static() bool {
	return p.Interpreter == "" && len(p.Needed) == 0
}

// InspectProgram reads what the native file at path is as a program. A file
// that is neither ELF nor Mach-O is an error wrapping ErrNotNative.
func InspectProgram(path string) (Program, error) {
	platforms, err := FilePlatforms(path)
	if err != nil {
		return Program{}, err
	}

	if platforms == nil {
		return Program{}, ErrNotNative
	}

	file, err := os.Open(path)
	if err != nil {
		return Program{}, err //nolint:wrapcheck // The caller names the file.
	}

	defer func() { _ = file.Close() }()

	prog := Program{Platforms: platforms}

	head := make([]byte, len(elf.ELFMAG))

	_, err = file.ReadAt(head, 0)
	if err != nil {
		return Program{}, err //nolint:wrapcheck // The caller names the file.
	}

	if bytes.Equal(head, []byte(elf.ELFMAG)) {
		err = inspectELF(file, &prog)
	} else {
		err = inspectMachO(file, &prog)
	}

	if err != nil {
		return Program{}, err
	}

	return prog, nil
}

// inspectELF fills prog from the program headers of an ELF file: PT_INTERP
// names the loader, and the dynamic segment PT_DYNAMIC points at lists the
// libraries and the flags.
//
// The program headers are read rather than the sections, as the loader reads
// them: a stripped program may have no section headers at all.
func inspectELF(src io.ReaderAt, prog *Program) error {
	file, err := elf.NewFile(src)
	if err != nil {
		return fmt.Errorf("reading the ELF headers: %w", err)
	}

	var dynamic *elf.Prog

	for _, segment := range file.Progs {
		switch segment.Type {
		case elf.PT_INTERP:
			data, err := io.ReadAll(segment.Open())
			if err != nil {
				return fmt.Errorf("reading the interpreter: %w", err)
			}

			prog.Interpreter = strings.TrimRight(string(data), "\x00")
		case elf.PT_DYNAMIC:
			dynamic = segment
		default:
		}
	}

	var flags1 uint64

	if dynamic != nil {
		prog.Needed, flags1, err = readDynamic(src, file, dynamic)
		if err != nil {
			return err
		}
	}

	// A position-independent executable is ET_DYN, as a shared library is.
	// It is told by naming a loader or, for a static-pie one, which names
	// none, by DF_1_PIE: what file(1) tells them by too.
	switch file.Type {
	case elf.ET_EXEC:
		prog.Executable = true
	case elf.ET_DYN:
		prog.Executable = prog.Interpreter != "" || flags1&uint64(elf.DF_1_PIE) != 0
	default:
		prog.Executable = false
	}

	return nil
}

// Sizes of a dynamic segment entry, a tag and a value of the word size of
// the file's class.
const (
	dynEntry64Len = 16
	dynEntry32Len = 8
)

// dynEntry is one entry of the dynamic segment, its tag and its value,
// whatever the class of the file. The tag stays a word as the file holds it
// and is compared with the elf.DynTag constants widened to it: a tag the
// standard library does not know may not fit an elf.DynTag.
type dynEntry struct {
	tag uint64
	val uint64
}

// readDynamic reads the dynamic segment of an ELF file: the names of the
// libraries it needs and its DT_FLAGS_1.
func readDynamic(src io.ReaderAt, file *elf.File, dynamic *elf.Prog) ([]string, uint64, error) {
	data, err := io.ReadAll(dynamic.Open())
	if err != nil {
		return nil, 0, fmt.Errorf("reading the dynamic segment: %w", err)
	}

	entries := dynEntries(data, file.Class, file.ByteOrder)

	var (
		names  []uint64
		strtab uint64
		flags1 uint64
	)

	for _, entry := range entries {
		switch entry.tag {
		case uint64(elf.DT_NEEDED):
			names = append(names, entry.val)
		case uint64(elf.DT_STRTAB):
			strtab = entry.val
		case uint64(elf.DT_FLAGS_1):
			flags1 = entry.val
		}
	}

	needed := make([]string, 0, len(names))

	for _, name := range names {
		needed = append(needed, libraryName(src, file, strtab, name))
	}

	return needed, flags1, nil
}

// dynEntries decodes the entries of a dynamic segment up to DT_NULL.
func dynEntries(data []byte, class elf.Class, order binary.ByteOrder) []dynEntry {
	var entries []dynEntry

	for {
		var entry dynEntry

		switch class {
		case elf.ELFCLASS64:
			if len(data) < dynEntry64Len {
				return entries
			}

			entry = dynEntry{order.Uint64(data), order.Uint64(data[dynEntry64Len/2:])}
			data = data[dynEntry64Len:]
		case elf.ELFCLASS32:
			if len(data) < dynEntry32Len {
				return entries
			}

			entry = dynEntry{
				uint64(order.Uint32(data)),
				uint64(order.Uint32(data[dynEntry32Len/2:])),
			}
			data = data[dynEntry32Len:]
		default:
			return entries
		}

		if entry.tag == uint64(elf.DT_NULL) {
			return entries
		}

		entries = append(entries, entry)
	}
}

// libraryName reads the name of a needed library at offset name of the
// string table at the address strtab. The table is found through the
// loadable segments, as the loader finds it. A name that cannot be read is
// still a library the program needs, so it is reported by its offset rather
// than dropped.
func libraryName(src io.ReaderAt, file *elf.File, strtab, name uint64) string {
	unnamed := fmt.Sprintf("a library at offset %d of the string table", name)

	for _, segment := range file.Progs {
		if segment.Type != elf.PT_LOAD ||
			strtab < segment.Vaddr || strtab >= segment.Vaddr+segment.Filesz {
			continue
		}

		off := segment.Off + (strtab - segment.Vaddr) + name
		if off > math.MaxInt64 {
			return unnamed
		}

		text, ok := readCString(src, int64(off))
		if ok {
			return text
		}

		return unnamed
	}

	return unnamed
}

// readCString reads the NUL-terminated string at off.
func readCString(src io.ReaderAt, off int64) (string, bool) {
	var text []byte

	chunk := make([]byte, cStringChunk)

	for {
		read, err := src.ReadAt(chunk, off)
		if end := bytes.IndexByte(chunk[:read], 0); end >= 0 {
			return string(append(text, chunk[:end]...)), true
		}

		if err != nil {
			return "", false
		}

		text = append(text, chunk[:read]...)

		off += int64(read)
	}
}

// inspectMachO fills prog from a Mach-O file: a thin one, or a universal one
// whose every slice has to be an executable. A Mach-O program always loads
// through dyld, which it does not name the way an ELF one names its loader,
// so only its libraries are reported.
func inspectMachO(src io.ReaderAt, prog *Program) error {
	fat, err := macho.NewFatFile(src)
	if err == nil {
		defer func() { _ = fat.Close() }()

		prog.Executable = len(fat.Arches) > 0

		for _, arch := range fat.Arches {
			prog.Executable = prog.Executable && arch.Type == macho.TypeExec
		}

		if len(fat.Arches) > 0 {
			prog.Needed, _ = fat.Arches[0].ImportedLibraries()
		}

		return nil
	}

	if !errors.Is(err, macho.ErrNotFat) {
		return fmt.Errorf("reading the Mach-O headers: %w", err)
	}

	thin, err := macho.NewFile(src)
	if err != nil {
		return fmt.Errorf("reading the Mach-O headers: %w", err)
	}

	defer func() { _ = thin.Close() }()

	prog.Executable = thin.Type == macho.TypeExec
	prog.Needed, _ = thin.ImportedLibraries()

	return nil
}
