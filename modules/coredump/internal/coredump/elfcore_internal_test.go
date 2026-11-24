package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPage is the size of a page of the test images and cores.
const testPage = 0x1000

// testExecBase is where a test executable that is not position-independent
// is linked, as ld links one for x86_64.
const testExecBase = 0x400000

// testSymbol is a symbol of a test ELF file, defined in the section of its
// name.
type testSymbol struct {
	name    string
	section string
}

// testObject is a test ELF file: a little-endian 64-bit file of type typ,
// mapped from its first byte at base. Its first page has the header, the
// program headers, PT_PHDR, PT_LOAD of the whole file, PT_NOTE when there
// are notes, PT_DYNAMIC of testDynamicSize bytes at the address dynamic
// when it is not 0 and PT_INTERP when there is an interpreter, then the
// notes and the interpreter; the sections follow it: .text, .data, and the
// table of symbols, of type table, with its strings.
type testObject struct {
	typ     elf.Type
	base    uint64
	entry   uint64
	notes   []byte
	dynamic uint64
	interp  string
	table   elf.SectionType
	symbols []testSymbol
}

// testDynamicSize is the size of the dynamic section of a test object.
const testDynamicSize = 0x40

// phnum returns the number of the program headers of the file.
func (o testObject) phnum() uint64 {
	phnum := uint64(2)
	if o.notes != nil {
		phnum++
	}

	if o.dynamic != 0 {
		phnum++
	}

	if o.interp != "" {
		phnum++
	}

	return phnum
}

// interpOffset returns the offset in the file of the interpreter, after
// the header, the program headers and the notes.
func (o testObject) interpOffset() uint64 {
	const (
		headerSize = 64
		progSize   = 56
	)

	return headerSize + o.phnum()*progSize + uint64(len(o.notes))
}

// bytes returns the content of the file.
//
//nolint:gosec // The sizes are those of test data.
func (o testObject) bytes(t *testing.T) []byte {
	t.Helper()

	const (
		headerSize = 64
		progSize   = 56
		symSize    = 24
	)

	phnum := o.phnum()
	noteOffset := headerSize + phnum*progSize
	interpOffset := o.interpOffset()
	interpSize := uint64(len(o.interp) + 1)
	require.LessOrEqual(t, interpOffset+interpSize, uint64(testPage))

	// The sections after the first page: .text, .data, the symbols and
	// their strings, the names of the sections, then the headers.
	type section struct {
		name       string
		typ        elf.SectionType
		data       []byte
		link, info uint32
		entsize    uint64
	}

	sections := []section{
		{
			name: ".text", typ: elf.SHT_PROGBITS, data: bytes.Repeat([]byte{0x90}, 16), link: 0,
			info: 0, entsize: 0,
		},
		{
			name: ".data", typ: elf.SHT_PROGBITS, data: make([]byte, 16), link: 0, info: 0,
			entsize: 0,
		},
	}

	if o.symbols != nil {
		strtab := []byte{0}

		var symtab bytes.Buffer

		symtab.Write(make([]byte, symSize))

		for _, symbol := range o.symbols {
			index := 1
			if symbol.section == ".data" {
				index = 2
			}

			name := uint32(len(strtab))

			strtab = append(strtab, []byte(symbol.name+"\x00")...)

			require.NoError(t, binary.Write(&symtab, binary.LittleEndian, elf.Sym64{
				Name: name, Info: elf.ST_INFO(elf.STB_GLOBAL, elf.STT_FUNC),
				Shndx: uint16(index), Value: o.base + testPage,
			}))
		}

		names, name := ".dynstr", ".dynsym"
		if o.table == elf.SHT_SYMTAB {
			names, name = ".strtab", ".symtab"
		}

		// The table of symbols links the section of their names, the
		// third one after the null section.
		sections = append(sections,
			section{
				name: names, typ: elf.SHT_STRTAB, data: strtab, link: 0, info: 0, entsize: 0,
			},
			section{
				name: name, typ: o.table, data: symtab.Bytes(), link: 3, info: 1,
				entsize: symSize,
			})
	}

	shstrtab := []byte{0}
	for _, section := range sections {
		shstrtab = append(shstrtab, []byte(section.name+"\x00")...)
	}

	shstrtab = append(shstrtab, []byte(".shstrtab\x00")...)
	sections = append(sections, section{
		name: ".shstrtab", typ: elf.SHT_STRTAB, data: shstrtab, link: 0, info: 0, entsize: 0,
	})

	var content bytes.Buffer

	// The null section, then the sections.
	headers := make([]elf.Section64, 1, 1+len(sections))
	nameOffset := uint32(1)

	for _, section := range sections {
		offset := uint64(testPage + content.Len())

		headers = append(headers, elf.Section64{
			Name: nameOffset, Type: uint32(section.typ), Addr: o.base + offset, Off: offset,
			Size: uint64(len(section.data)), Link: section.link, Info: section.info,
			Addralign: 1, Entsize: section.entsize,
		})

		nameOffset += uint32(len(section.name) + 1)

		content.Write(section.data)
	}

	shoff := uint64(testPage + content.Len())
	size := shoff + uint64(len(headers))*64

	var file bytes.Buffer

	require.NoError(t, binary.Write(&file, binary.LittleEndian, elf.Header64{
		Ident: [16]byte{
			0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB),
			byte(elf.EV_CURRENT),
		},
		Type: uint16(o.typ), Machine: uint16(elf.EM_X86_64), Version: uint32(elf.EV_CURRENT),
		Entry: o.entry, Phoff: headerSize, Shoff: shoff, Ehsize: headerSize,
		Phentsize: progSize, Phnum: uint16(phnum), Shentsize: 64,
		Shnum: uint16(len(headers)), Shstrndx: uint16(len(headers) - 1),
	}))

	progs := []elf.Prog64{
		{
			Type: uint32(elf.PT_PHDR), Flags: uint32(elf.PF_R), Off: headerSize,
			Vaddr: o.base + headerSize, Paddr: o.base + headerSize, Filesz: phnum * progSize,
			Memsz: phnum * progSize, Align: 8,
		},
		{
			Type: uint32(elf.PT_LOAD), Flags: uint32(elf.PF_R | elf.PF_X), Off: 0,
			Vaddr: o.base, Paddr: o.base, Filesz: size, Memsz: size, Align: testPage,
		},
	}
	if o.notes != nil {
		progs = append(progs, elf.Prog64{
			Type: uint32(elf.PT_NOTE), Flags: uint32(elf.PF_R), Off: noteOffset,
			Vaddr: o.base + noteOffset, Paddr: o.base + noteOffset,
			Filesz: uint64(len(o.notes)), Memsz: uint64(len(o.notes)), Align: 4,
		})
	}

	// The dynamic section is in memory alone, as one that is written to:
	// the core holds it.
	if o.dynamic != 0 {
		progs = append(progs, elf.Prog64{
			Type: uint32(elf.PT_DYNAMIC), Flags: uint32(elf.PF_R | elf.PF_W), Off: 0,
			Vaddr: o.dynamic, Paddr: o.dynamic, Filesz: 0, Memsz: testDynamicSize, Align: 8,
		})
	}

	if o.interp != "" {
		progs = append(progs, elf.Prog64{
			Type: uint32(elf.PT_INTERP), Flags: uint32(elf.PF_R), Off: interpOffset,
			Vaddr: o.base + interpOffset, Paddr: o.base + interpOffset, Filesz: interpSize,
			Memsz: interpSize, Align: 1,
		})
	}

	require.NoError(t, binary.Write(&file, binary.LittleEndian, progs))
	file.Write(o.notes)

	if o.interp != "" {
		file.WriteString(o.interp + "\x00")
	}

	file.Write(make([]byte, testPage-file.Len()))
	file.Write(content.Bytes())
	require.NoError(t, binary.Write(&file, binary.LittleEndian, headers))

	return file.Bytes()
}

// write writes the file to path and returns path.
func (o testObject) write(t *testing.T, path string) string {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, o.bytes(t), 0o755))

	return path
}

// testExecutable returns a test executable of the build ID id, none when id
// is empty, mapped at base and entered at base+entry.
func testExecutable(t *testing.T, base, entry uint64, id string) testObject {
	t.Helper()

	notes := note("GNU", 1, []byte{0, 0, 0, 0, 3, 0, 0, 0})
	if id != "" {
		notes = append(notes, buildIDNote(t, id)...)
	}

	// An executable linked at testExecBase, or a position-independent one,
	// whose addresses are those of the file, mapped anywhere.
	if base == testExecBase {
		return testObject{
			typ: elf.ET_EXEC, base: base, entry: base + entry, notes: notes, dynamic: 0,
			table:   elf.SHT_DYNSYM,
			symbols: []testSymbol{{name: tarantoolSignature, section: ".text"}},
		}
	}

	return testObject{
		typ: elf.ET_DYN, base: 0, entry: entry, notes: notes, dynamic: 0, table: elf.SHT_DYNSYM,
		symbols: []testSymbol{{name: tarantoolSignature, section: ".text"}},
	}
}

// testSegment is a segment of memory of a test core: data at vaddr, of the
// permissions flags, read and write when they are 0, and of size bytes of
// memory, len(data) when it is less, of which the core holds data alone.
type testSegment struct {
	vaddr uint64
	data  []byte
	flags elf.ProgFlag
	size  uint64
}

// testCore is a test core dump, little-endian and 64-bit: its segments of
// memory, the files it maps and its auxiliary vector, in the NT_FILE and
// NT_AUXV notes, and more notes.
type testCore struct {
	segments []testSegment
	mappings []fileMapping
	auxv     [][2]uint64
	notes    []byte
}

// fileNote returns the NT_FILE note of mappings, of pages of testPage.
func fileNote(mappings []fileMapping) []byte {
	var desc bytes.Buffer

	words := make([]uint64, 0, 2+3*len(mappings))

	words = append(words, uint64(len(mappings)), testPage)

	for _, mapping := range mappings {
		words = append(words, mapping.start, mapping.end, mapping.offset/testPage)
	}

	_ = binary.Write(&desc, binary.LittleEndian, words)

	for _, mapping := range mappings {
		desc.WriteString(mapping.path + "\x00")
	}

	return note("CORE", ntFile, desc.Bytes())
}

// auxvNote returns the NT_AUXV note of the entries auxv, ended by AT_NULL.
func auxvNote(auxv [][2]uint64) []byte {
	var desc bytes.Buffer

	for _, entry := range auxv {
		_ = binary.Write(&desc, binary.LittleEndian, entry)
	}

	_ = binary.Write(&desc, binary.LittleEndian, [2]uint64{atNull, 0})

	return note("CORE", ntAuxv, desc.Bytes())
}

// write writes the core to path and returns path.
//
//nolint:gosec // The sizes are those of test data.
func (c testCore) write(t *testing.T, path string) string {
	t.Helper()

	const (
		headerSize = 64
		progSize   = 56
	)

	notes := append(append(fileNote(c.mappings), auxvNote(c.auxv)...), c.notes...)

	phnum := uint64(1 + len(c.segments))
	offset := headerSize + phnum*progSize

	progs := make([]elf.Prog64, 0, phnum)

	progs = append(progs, elf.Prog64{
		Type: uint32(elf.PT_NOTE), Off: offset, Filesz: uint64(len(notes)), Align: 4,
	})

	offset += uint64(len(notes))

	for _, segment := range c.segments {
		flags := segment.flags
		if flags == 0 {
			flags = elf.PF_R | elf.PF_W
		}

		progs = append(progs, elf.Prog64{
			Type: uint32(elf.PT_LOAD), Flags: uint32(flags), Off: offset,
			Vaddr: segment.vaddr, Filesz: uint64(len(segment.data)),
			Memsz: max(segment.size, uint64(len(segment.data))), Align: testPage,
		})

		offset += uint64(len(segment.data))
	}

	var file bytes.Buffer

	require.NoError(t, binary.Write(&file, binary.LittleEndian, elf.Header64{
		Ident: [16]byte{
			0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB),
			byte(elf.EV_CURRENT),
		},
		Type: uint16(elf.ET_CORE), Machine: uint16(elf.EM_X86_64),
		Version: uint32(elf.EV_CURRENT), Phoff: headerSize, Ehsize: headerSize,
		Phentsize: progSize, Phnum: uint16(phnum),
	}))
	require.NoError(t, binary.Write(&file, binary.LittleEndian, progs))
	file.Write(notes)

	for _, segment := range c.segments {
		file.Write(segment.data)
	}

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, file.Bytes(), 0o600))

	return path
}

// open writes the core into a temporary directory and opens it.
func (c testCore) open(t *testing.T) *coreDump {
	t.Helper()

	core, err := openCore(c.write(t, filepath.Join(t.TempDir(), "core")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = core.Close() })

	return core
}

// testExecutableCore returns a test core of the executable exe mapped at
// base: its first page, as Linux dumps it, mapped from the file of path,
// and the rest of the file mapped without content.
func testExecutableCore(t *testing.T, exe testObject, base uint64, path string) testCore {
	t.Helper()

	image := exe.bytes(t)

	return testCore{
		segments: []testSegment{{vaddr: base, data: image[:testPage]}},
		mappings: []fileMapping{
			{start: base, end: base + testPage, offset: 0, path: path},
			{start: base + testPage, end: base + 4*testPage, offset: testPage, path: path},
		},
		auxv:  [][2]uint64{{atPhdr, base + 64}},
		notes: nil,
	}
}

func TestParseNotes(t *testing.T) {
	t.Parallel()

	data := append(note("CORE", ntFile, []byte{1, 2, 3, 4}), note("GNU", 1, []byte{5, 6, 7})...)
	assert.Equal(t, []elfNote{
		{name: "CORE\x00", typ: ntFile, desc: []byte{1, 2, 3, 4}},
		{name: "GNU\x00", typ: 1, desc: []byte{5, 6, 7}},
	}, parseNotes(data, binary.LittleEndian, 4))

	// A last note without the padding of its descriptor is whole.
	assert.Len(t, parseNotes(data[:len(data)-1], binary.LittleEndian, 1), 2)

	// A note cut short ends the list.
	cut := note("CORE", ntFile, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	assert.Empty(t, parseNotes(cut[:len(cut)-1], binary.LittleEndian, 4))
	assert.Empty(t, parseNotes(nil, binary.LittleEndian, 4))

	// The notes of a segment of 8-byte alignment, as .note.gnu.property
	// of x86_64 and aarch64 is: the descriptor of each starts at the
	// header and the name padded together, 16 bytes in for "GNU", and the
	// next note after the descriptor padded.
	property := bytes.Repeat([]byte{0xaa}, 0x30)
	build := bytes.Repeat([]byte{0xbb}, 20)
	aligned := append(note8("GNU", 5, property), note8("GNU", ntGNUBuildID, build)...)
	assert.Equal(t, []elfNote{
		{name: "GNU\x00", typ: 5, desc: property},
		{name: "GNU\x00", typ: ntGNUBuildID, desc: build},
	}, parseNotes(aligned, binary.LittleEndian, 8))
	assert.Equal(t, hex.EncodeToString(build), noteBuildID(aligned, binary.LittleEndian, 8))
}

// note8 returns an ELF note of name, typ and desc of a segment of 8-byte
// alignment: the descriptor starts at the header and the name padded to 8
// bytes, and is padded to 8 bytes.
//
//nolint:gosec // The sizes are those of test data.
func note8(name string, typ uint32, desc []byte) []byte {
	var buf bytes.Buffer

	pad := func() {
		for buf.Len()%8 != 0 {
			buf.WriteByte(0)
		}
	}

	_ = binary.Write(&buf, binary.LittleEndian, []uint32{
		uint32(len(name) + 1), uint32(len(desc)), typ,
	})

	buf.WriteString(name + "\x00")
	pad()
	buf.Write(desc)
	pad()

	return buf.Bytes()
}

func TestParseFileNote(t *testing.T) {
	t.Parallel()

	mappings := []fileMapping{
		{start: 0x400000, end: 0x401000, offset: 0, path: "/usr/bin/tarantool"},
		{start: 0x401000, end: 0x500000, offset: 2 * testPage, path: "/usr/bin/tarantool"},
		{start: 0x7f0000, end: 0x7f1000, offset: 0, path: "/tmp/m.so (deleted)"},
	}

	core := testCore{segments: nil, mappings: mappings, auxv: nil, notes: nil}.open(t)
	assert.Equal(t, mappings, core.mappings)

	desc := parseNotes(fileNote(mappings), binary.LittleEndian, 4)[0].desc

	// More files than the note has room for, and fewer paths than files.
	tooMany := bytes.Clone(desc)
	binary.LittleEndian.PutUint64(tooMany, 1000)

	_, err := core.parseFileNote(tooMany)
	require.ErrorIs(t, err, errMalformedNote)

	_, err = core.parseFileNote(desc[:len(desc)-1])
	require.ErrorIs(t, err, errMalformedNote)

	_, err = core.parseFileNote(desc[:8])
	require.ErrorIs(t, err, errMalformedNote)

	// A core of a big-endian 32-bit process has notes of 4-byte words.
	var big bytes.Buffer

	_ = binary.Write(&big, binary.BigEndian, []uint32{1, testPage, 0x10000, 0x12000, 3})
	big.WriteString("/lib/libc.so.6\x00")

	core32 := &coreDump{
		file: &elf.File{
			Class: elf.ELFCLASS32, Data: elf.ELFDATA2MSB, Version: elf.EV_CURRENT,
			OSABI: elf.ELFOSABI_NONE, ABIVersion: 0, ByteOrder: binary.BigEndian,
			Type: elf.ET_CORE, Machine: elf.EM_PPC, Entry: 0,
		},
		loads: nil, mappings: nil, auxv: map[uint64]uint64{},
	}

	got, err := core32.parseFileNote(big.Bytes())
	require.NoError(t, err)
	assert.Equal(t, []fileMapping{
		{start: 0x10000, end: 0x12000, offset: 3 * testPage, path: "/lib/libc.so.6"},
	}, got)

	// The entries of the auxiliary vector up to AT_NULL.
	big.Reset()

	_ = binary.Write(&big, binary.BigEndian, []uint32{atPhdr, 0x10034, 9, 0x10400, atNull, 0, 5})
	core32.parseAuxv(big.Bytes())
	assert.Equal(t, map[uint64]uint64{atPhdr: 0x10034, 9: 0x10400}, core32.auxv)
}

func TestOpenCore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := openCore(filepath.Join(dir, "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// An ELF file of another type.
	_, err = openCore(testObject{
		typ: elf.ET_EXEC, base: 0x400000, entry: 0, notes: nil, dynamic: 0, table: 0, symbols: nil,
	}.write(t, filepath.Join(dir, "exe")))
	require.ErrorIs(t, err, errNotCoreFile)

	// A core whose NT_FILE note is cut short is a core without the files it
	// maps, as gdb and file(1) take it, which says why.
	path := testCore{
		segments: []testSegment{{vaddr: 0x1000, data: []byte("data")}},
		mappings: nil, auxv: [][2]uint64{{atPhdr, 0x1040}},
		notes: note("CORE", ntFile, []byte{1, 2, 3}),
	}.write(t, filepath.Join(dir, "core"))

	core, err := openCore(path)
	require.NoError(t, err)
	assert.Empty(t, core.mappings)
	require.ErrorIs(t, core.fileNoteErr, errMalformedNote)
	assert.Equal(t, map[uint64]uint64{atPhdr: 0x1040}, core.auxv)
	assert.Len(t, core.loads, 1)
	require.NoError(t, core.Close())

	require.NoError(t, os.WriteFile(path, []byte("not an ELF file"), 0o600))

	_, err = openCore(path)
	require.Error(t, err)
}

func TestCoreRead(t *testing.T) {
	t.Parallel()

	core := testCore{
		segments: []testSegment{
			{vaddr: 0x2000, data: []byte("efgh")},
			{vaddr: 0x1ffc, data: []byte("abcd")},
			{vaddr: 0x3000, data: []byte("ijkl")},
		},
		mappings: nil, auxv: nil, notes: nil,
	}.open(t)

	// Across adjacent segments, given out of order.
	data, err := core.read(0x1ffe, 5)
	require.NoError(t, err)
	assert.Equal(t, "cdefg", string(data))

	data, err = core.read(0x3001, 3)
	require.NoError(t, err)
	assert.Equal(t, "jkl", string(data))

	for _, addr := range []uint64{0x1000, 0x2002, 0x3003, 0x4000} {
		_, err = core.read(addr, 4)
		require.ErrorIs(t, err, errNotInCore, "%#x", addr)
	}
}

func TestCoreReadBinary(t *testing.T) {
	t.Parallel()

	const bias = 0x7f0000000000

	// The core holds a page the process wrote to in a read-only segment of
	// the binary, as text relocations do.
	core := testCore{
		segments: []testSegment{{vaddr: bias + 0x1008, data: []byte("core")}},
		mappings: nil, auxv: nil, notes: nil,
	}.open(t)

	segment := func(flags elf.ProgFlag, vaddr uint64, data string) *elf.Prog {
		return &elf.Prog{
			Type: elf.PT_LOAD, Flags: flags, Off: 0, Vaddr: vaddr, Paddr: vaddr,
			Filesz: uint64(len(data)), Memsz: uint64(len(data)) + 0x100, Align: testPage,
			ReaderAt: strings.NewReader(data),
		}
	}

	binary := &elf.File{Progs: []*elf.Prog{
		segment(elf.PF_R, 0x1000, "/lib64/ld-linux-x86-64.so.2\x00"),
		segment(elf.PF_R|elf.PF_W, 0x2000, "/data/written\x00"),
		segment(elf.PF_R|elf.PF_X, 0x3000, "text"),
		{Type: elf.PT_NOTE, Vaddr: 0x4000, Filesz: 4},
	}}

	view := core.withBinary(binary, bias)

	// The memory of the read-only segments of the binary, but where the
	// core has content, up to the end of the file content of a segment.
	data, err := view.read(bias+0x1000, 16)
	require.NoError(t, err)
	assert.Equal(t, "/lib64/lcorenux-", string(data))

	data, err = view.read(bias+0x3000, 4)
	require.NoError(t, err)
	assert.Equal(t, "text", string(data))

	name, err := view.readString(bias + 0x1000)
	require.NoError(t, err)
	assert.Equal(t, "/lib64/lcorenux-x86-64.so.2", name)

	name, err = view.readString(bias + 0x100c)
	require.NoError(t, err)
	assert.Equal(t, "nux-x86-64.so.2", name)

	// Not that of a segment the process may have written to, nor past the
	// content of the file, nor another segment than PT_LOAD.
	for _, addr := range []uint64{0x2000, 0x1000 + 28, 0x3004, 0x4000} {
		_, err = view.read(bias+addr, 1)
		require.ErrorIs(t, err, errNotInCore, "%#x", addr)
	}

	// The core itself holds the page alone.
	_, err = core.read(bias+0x1000, 1)
	require.ErrorIs(t, err, errNotInCore)
}

// TestCorePID checks the PID of the process of a core, which names the
// archive of pack: pr_pid of its NT_PRPSINFO note, and none of a core
// without the note, with a short one or one of no PID, or of a 32-bit
// process, whose note has another layout.
func TestCorePID(t *testing.T) {
	t.Parallel()

	core := testCore{segments: nil, mappings: nil, auxv: nil, notes: prpsinfoNote(4242)}.open(t)

	pid, ok := core.pid()
	assert.True(t, ok)
	assert.Equal(t, uint(4242), pid)

	// The layout of a 32-bit process is not read.
	core.file.Class = elf.ELFCLASS32

	pid, ok = core.pid()
	assert.False(t, ok)
	assert.Zero(t, pid)

	cases := map[string][]byte{
		"no note": nil,
		"short":   note("CORE", ntPrpsinfo, make([]byte, testPrpsinfoPID+2)),
		"no pid":  prpsinfoNote(0),
	}

	for name, notes := range cases {
		pid, ok := testCore{segments: nil, mappings: nil, auxv: nil, notes: notes}.open(t).pid()
		assert.False(t, ok, name)
		assert.Zero(t, pid, name)
	}
}

func TestExecutableImage(t *testing.T) {
	t.Parallel()

	const pieBase = 0x7f1234560000

	for _, base := range []uint64{0x400000, pieBase} {
		exe := testExecutable(t, base, 0x123, testBuildID)
		core := testExecutableCore(t, exe, base, "/usr/bin/tarantool").open(t)

		image, err := core.executableImage()
		require.NoError(t, err)

		bias := uint64(0)
		if base == pieBase {
			bias = pieBase
		}

		assert.Equal(t, bias, image.bias, "%#x", base)
		assert.Equal(t, exe.entry, image.entry)
		require.Len(t, image.progs, 3)
		assert.Equal(t, elf.PT_PHDR, image.progs[0].Type)
		assert.Equal(t, testBuildID, core.imageBuildID(image))

		// The image of an executable without a build ID.
		core = testExecutableCore(t, testExecutable(t, base, 0x123, ""), base,
			"/usr/bin/tarantool").open(t)

		image, err = core.executableImage()
		require.NoError(t, err)
		assert.Empty(t, core.imageBuildID(image))
	}

	exe := testExecutable(t, 0x400000, 0x123, testBuildID)
	cases := map[string]func(*testCore){
		"no AT_PHDR": func(c *testCore) { c.auxv = nil },
		"no mapping": func(c *testCore) { c.mappings = nil },
		"not mapped from the start": func(c *testCore) {
			c.auxv = [][2]uint64{{atPhdr, 0x400000 + testPage + 64}}
		},
		"first page left out": func(c *testCore) { c.segments = nil },
		"no ELF header": func(c *testCore) {
			c.segments = []testSegment{{vaddr: 0x400000, data: make([]byte, testPage)}}
		},
	}

	for name, change := range cases {
		core := testExecutableCore(t, exe, 0x400000, "/usr/bin/tarantool")
		change(&core)

		_, err := core.open(t).executableImage()
		require.Error(t, err, name)
	}
}

// testDataBase is where a test executable linked at testExecBase has its
// dynamic section, and a test core the memory the loader writes: r_debug,
// the link map and the names of the objects.
const testDataBase = 0x600000

// testLinkMapCore returns a test core of exe, mapped from path at
// testExecBase with its dynamic section at testDataBase, whose DT_DEBUG
// points to an r_debug that heads a link map of objects. The last object
// links back to the first one when loop is set.
func testLinkMapCore(t *testing.T, exe testObject, path string, objects []linkMapEntry,
	loop bool,
) testCore {
	t.Helper()

	const (
		rDebug    = testDataBase + testDynamicSize
		linkMap   = rDebug + 0x10
		entrySize = 5 * 8
		names     = linkMap + 16*entrySize
	)

	require.Equal(t, uint64(testDataBase), exe.dynamic)
	require.LessOrEqual(t, len(objects), 16)

	core := testExecutableCore(t, exe, testExecBase, path)
	data := make([]byte, testPage)

	put := func(addr, value uint64) {
		binary.LittleEndian.PutUint64(data[addr-testDataBase:], value)
	}

	// DT_NEEDED, DT_DEBUG and DT_NULL.
	put(testDataBase, uint64(elf.DT_NEEDED))
	put(testDataBase+8, 1)
	put(testDataBase+16, uint64(elf.DT_DEBUG))
	put(testDataBase+24, rDebug)

	// r_version and r_map.
	put(rDebug, 1)
	put(rDebug+8, linkMap)

	name := uint64(names)

	for index, object := range objects {
		entry := linkMap + uint64(index)*entrySize

		copy(data[name-testDataBase:], object.name+"\x00")
		put(entry, object.addr)
		put(entry+8, name)
		put(entry+16, object.dynamic)

		switch {
		case index+1 < len(objects):
			put(entry+24, entry+entrySize)
		case loop:
			put(entry+24, linkMap)
		default:
		}

		if index > 0 {
			put(entry+32, entry-entrySize)
		}

		name += uint64(len(object.name)) + 1
	}

	core.segments = append(core.segments, testSegment{vaddr: testDataBase, data: data})

	return core
}

// setLinkMapName points l_name of the object index of the link map of
// core, a core of testLinkMapCore, to addr.
func setLinkMapName(core *testCore, index int, addr uint64) {
	// The first link_map follows r_debug, after the dynamic section.
	const linkMap = testDynamicSize + 0x10

	binary.LittleEndian.PutUint64(core.segments[1].data[linkMap+index*5*8+8:], addr)
}

// testLongLinkMapCore returns a test core of exe, as testLinkMapCore does,
// whose link map has count objects without names.
func testLongLinkMapCore(t *testing.T, exe testObject, count int) testCore {
	t.Helper()

	const (
		rDebug    = testDataBase + testDynamicSize
		linkMap   = rDebug + 0x10
		entrySize = 5 * 8
	)

	core := testExecutableCore(t, exe, testExecBase, "/usr/bin/tarantool")
	data := make([]byte, linkMap-testDataBase+count*entrySize)

	put := func(addr, value uint64) {
		binary.LittleEndian.PutUint64(data[addr-testDataBase:], value)
	}

	put(testDataBase, uint64(elf.DT_DEBUG))
	put(testDataBase+8, rDebug)
	put(rDebug, 1)
	put(rDebug+8, linkMap)

	for index := range uint64(count) { //nolint:gosec // A count of test entries.
		entry := linkMap + index*entrySize
		if index > 0 {
			put(entry+32, entry-entrySize)
		}

		if index+1 < uint64(count) {
			put(entry+24, entry+entrySize)
		}
	}

	core.segments = append(core.segments, testSegment{vaddr: testDataBase, data: data})

	return core
}

// testLinkMapExecutable returns a test executable linked at testExecBase
// with its dynamic section at testDataBase.
func testLinkMapExecutable(t *testing.T) testObject {
	t.Helper()

	exe := testExecutable(t, testExecBase, 0x123, testBuildID)

	exe.dynamic = testDataBase

	return exe
}

func TestLinkMap(t *testing.T) {
	t.Parallel()

	exe := testLinkMapExecutable(t)
	objects := []linkMapEntry{
		{name: "", dynamic: testDataBase},
		{name: "linux-vdso.so.1", addr: 0x7ffff7fc1000, dynamic: 0x7ffff7fc1400},
		{name: "/lib/x86_64-linux-gnu/libc.so.6", addr: 0x7ffff7c00000, dynamic: 0x7ffff7e00000},
		{name: "/lib64/ld-linux-x86-64.so.2", addr: 0x7ffff7fc3000, dynamic: 0x7ffff7ffd000},
	}

	core := testLinkMapCore(t, exe, "/usr/bin/tarantool", objects, false).open(t)

	image, err := core.executableImage()
	require.NoError(t, err)

	// The first object, the executable, is left out by its place, whatever
	// its name: musl names it by AT_EXECFN.
	got, err := core.linkMap(image)
	require.NoError(t, err)
	assert.Equal(t, objects[1:], got)

	// An object whose name the core does not hold is listed without it,
	// and the rest of the map is read: the whole map failed.
	unread := testLinkMapCore(t, exe, "/usr/bin/tarantool", objects, false)

	setLinkMapName(&unread, 2, 0x7fff00000000)

	got, err = unread.open(t).linkMap(image)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, objects[1], got[0])
	assert.Equal(t, objects[3], got[2])
	assert.Empty(t, got[1].name)
	require.ErrorIs(t, got[1].nameErr, errNotInCore)
	assert.Equal(t, objects[2].addr, got[1].addr)
	assert.Equal(t, objects[2].dynamic, got[1].dynamic)

	named := slices.Clone(objects)

	named[0].name = "/usr/bin/tarantool"

	got, err = testLinkMapCore(t, exe, "/usr/bin/tarantool", named, false).open(t).linkMap(image)
	require.NoError(t, err)
	assert.Equal(t, objects[1:], got)

	// Nor is its name read: glibc 2.43 points it into the read-only data of
	// the loader that runs it as ld.so PROGRAM, which a core does not hold.
	unnamed := testLinkMapCore(t, exe, "/usr/bin/tarantool", objects, false)

	setLinkMapName(&unnamed, 0, 0x7ffff7ff30f4)

	got, err = unnamed.open(t).linkMap(image)
	require.NoError(t, err)
	assert.Equal(t, objects[1:], got)

	_, err = testLinkMapCore(t, exe, "/usr/bin/tarantool", objects, true).open(t).linkMap(image)
	require.ErrorIs(t, err, errLinkMapLoop)
	require.NotErrorIs(t, err, errLinkMapLong)

	// A link map of more objects than any process loads, which may not
	// end, is another error.
	_, err = testLongLinkMapCore(t, exe, maxLinkMap+1).open(t).linkMap(image)
	require.ErrorIs(t, err, errLinkMapLong)
	require.NotErrorIs(t, err, errLinkMapLoop)
	require.EqualError(t, err, "the link map is too long: more than 65536 objects")

	got, err = testLongLinkMapCore(t, exe, maxLinkMap).open(t).linkMap(image)
	require.NoError(t, err)
	assert.Len(t, got, maxLinkMap-1)

	// The memory the loader wrote left out of the core.
	partial := testLinkMapCore(t, exe, "/usr/bin/tarantool", objects, false)

	partial.segments = partial.segments[:1]

	_, err = partial.open(t).linkMap(image)
	require.ErrorIs(t, err, errNotInCore)

	// An executable without a dynamic section, as a static one, has no
	// link map.
	static := testExecutableCore(t, testExecutable(t, testExecBase, 0x123, testBuildID),
		testExecBase, "/usr/bin/tarantool").open(t)

	image, err = static.executableImage()
	require.NoError(t, err)

	got, err = static.linkMap(image)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestReadString(t *testing.T) {
	t.Parallel()

	long := bytes.Repeat([]byte{'a'}, maxPathSize+1)
	core := testCore{
		segments: []testSegment{
			{vaddr: 0x1000, data: []byte("/usr/li")},
			{vaddr: 0x1007, data: []byte("b/libc.so.6\x00rest")},
			{vaddr: 0x3000, data: []byte("no NUL")},
			{vaddr: 0x4000, data: long},
		},
		mappings: nil, auxv: nil, notes: nil,
	}.open(t)

	name, err := core.readString(0x1000)
	require.NoError(t, err)
	assert.Equal(t, "/usr/lib/libc.so.6", name)

	name, err = core.readString(0)
	require.NoError(t, err)
	assert.Empty(t, name)

	_, err = core.readString(0x3000)
	require.ErrorIs(t, err, errNotInCore)

	_, err = core.readString(0x4000)
	require.ErrorIs(t, err, errLongString)
}

func TestBinaryImage(t *testing.T) {
	t.Parallel()

	const base = 0x7f0000000000

	exe := testExecutable(t, base, 0x123, testBuildID)
	binary := openObject(t, exe)

	// A core that holds no page of the executable.
	core := testExecutableCore(t, exe, base, "/usr/bin/tarantool")

	core.segments = nil

	image, err := core.open(t).binaryImage(binary)
	require.NoError(t, err)
	assert.Equal(t, uint64(base), image.bias)
	assert.Equal(t, uint64(0x123), image.entry)
	assert.Len(t, image.progs, 3)

	core.auxv = nil

	_, err = core.open(t).binaryImage(binary)
	require.ErrorIs(t, err, errNoImage)

	// The kernel ran the dynamic loader, ld.so BINARY, and loaded no
	// interpreter: AT_PHDR is that of the loader.
	exe.interp = testLoaderName
	core.auxv = [][2]uint64{{atPhdr, 0x7ffff7fc0040}, {atBase, 0}}

	_, err = core.open(t).binaryImage(openObject(t, exe))
	require.ErrorIs(t, err, errNoImage)

	core.auxv = [][2]uint64{{atPhdr, base + 64}, {atBase, 0x7ffff7fc0000}}

	image, err = core.open(t).binaryImage(openObject(t, exe))
	require.NoError(t, err)
	assert.Equal(t, uint64(base), image.bias)

	// An executable that is not position-independent is where its headers
	// say, whatever AT_PHDR is.
	fixed := testExecutable(t, testExecBase, 0x123, testBuildID)

	core = testExecutableCore(t, fixed, testExecBase, "/usr/bin/tarantool")

	core.segments = nil
	core.auxv = [][2]uint64{{atPhdr, 0x7ffff7fc0040}, {atBase, 0}}

	image, err = core.open(t).binaryImage(openObject(t, fixed))
	require.NoError(t, err)
	assert.Equal(t, uint64(0), image.bias)
	assert.Equal(t, uint64(testExecBase+0x123), image.entry)
}
