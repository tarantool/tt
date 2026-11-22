package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
)

// testBuildID is the build ID of the test ELF files.
const testBuildID = "781dc56a9fb4e0f2e457995160fea91d4cd3fa72"

// testSection is a section of a test ELF file.
type testSection struct {
	name  string
	typ   elf.SectionType
	data  []byte
	align uint64
}

// note returns an ELF note of name, typ and desc, padded to 4 bytes.
//
//nolint:gosec // The sizes are those of test data.
func note(name string, typ uint32, desc []byte) []byte {
	var buf bytes.Buffer

	pad := func() {
		for buf.Len()%4 != 0 {
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

// buildIDNote returns the GNU build ID note of the build ID id in hex.
func buildIDNote(t *testing.T, id string) []byte {
	t.Helper()

	desc, err := hex.DecodeString(id)
	require.NoError(t, err)

	return note("GNU", ntGNUBuildID, desc)
}

// writeELF writes a little-endian 64-bit ELF file of sections to path, and
// a note segment of notes when it is not nil.
//
//nolint:gosec // The sizes are those of test data.
func writeELF(t *testing.T, path string, sections []testSection, notes []byte) {
	t.Helper()

	const (
		headerSize  = 64
		progSize    = 56
		sectionSize = 64
	)

	// The layout: the header, the note segment, the content of the
	// sections, the names of the sections, then the section headers.
	phnum, noteOffset, offset := uint16(0), uint64(0), uint64(headerSize)

	if notes != nil {
		phnum, noteOffset = 1, headerSize+progSize
		offset = noteOffset + uint64(len(notes))
	}

	strtab := []byte{0}

	// The null section, the sections and the names of the sections.
	headers := make([]elf.Section64, 1, len(sections)+2)

	var data bytes.Buffer

	for _, section := range sections {
		size := uint64(len(section.data))

		headers = append(headers, elf.Section64{
			Name: uint32(len(strtab)), Type: uint32(section.typ), Off: offset, Size: size,
			Addralign: section.align,
		})
		strtab = append(strtab, []byte(section.name+"\x00")...)

		if section.typ != elf.SHT_NOBITS {
			data.Write(section.data)

			offset += size
		}
	}

	headers = append(headers, elf.Section64{
		Name: uint32(len(strtab)), Type: uint32(elf.SHT_STRTAB), Off: offset,
		Size: uint64(len(strtab) + len(".shstrtab\x00")), Addralign: 1,
	})
	strtab = append(strtab, []byte(".shstrtab\x00")...)

	data.Write(strtab)

	offset += uint64(len(strtab))

	var file bytes.Buffer

	header := elf.Header64{
		Ident: [16]byte{
			0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB),
			byte(elf.EV_CURRENT),
		},
		Type: uint16(elf.ET_EXEC), Machine: uint16(elf.EM_X86_64),
		Version: uint32(elf.EV_CURRENT), Phnum: phnum, Phentsize: progSize,
		Shoff: offset, Shnum: uint16(len(headers)), Shentsize: sectionSize,
		Shstrndx: uint16(len(headers) - 1), Ehsize: headerSize,
	}
	if notes != nil {
		header.Phoff = headerSize
	}

	require.NoError(t, binary.Write(&file, binary.LittleEndian, header))

	if notes != nil {
		require.NoError(t, binary.Write(&file, binary.LittleEndian, elf.Prog64{
			Type: uint32(elf.PT_NOTE), Flags: uint32(elf.PF_R), Off: noteOffset,
			Filesz: uint64(len(notes)), Memsz: uint64(len(notes)), Align: 4,
		}))
		file.Write(notes)
	}

	file.Write(data.Bytes())
	require.NoError(t, binary.Write(&file, binary.LittleEndian, headers))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, file.Bytes(), 0o755))
}

// openELF opens the test ELF file of sections and notes.
func openELF(t *testing.T, sections []testSection, notes []byte) *elf.File {
	t.Helper()

	path := filepath.Join(t.TempDir(), "elf")
	writeELF(t, path, sections, notes)

	file, err := elf.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })

	return file
}

// The sections of the test ELF files.
var (
	debugInfo = testSection{
		name: ".debug_info", typ: elf.SHT_PROGBITS, data: []byte("dwarf"), align: 1,
	}
	zdebugInfo = testSection{
		name: ".zdebug_info", typ: elf.SHT_PROGBITS, data: []byte("ZLIBdwarf"), align: 1,
	}
	strippedDebugInfo = testSection{
		name: ".debug_info", typ: elf.SHT_NOBITS, data: []byte("dwarf"), align: 1,
	}
	text = testSection{name: ".text", typ: elf.SHT_PROGBITS, data: []byte{0x90}, align: 16}
	// strippedText is the code of a separate debug file: it has none.
	strippedText = testSection{name: ".text", typ: elf.SHT_NOBITS, data: []byte{0x90}, align: 16}
)

// buildIDSection returns the build ID note section of testBuildID.
func buildIDSection(t *testing.T) testSection {
	t.Helper()

	return testSection{
		name: ".note.gnu.build-id", typ: elf.SHT_NOTE, data: buildIDNote(t, testBuildID),
		align: 4,
	}
}

func TestHasDWARF(t *testing.T) {
	t.Parallel()

	assert.True(t, hasDWARF(openELF(t, []testSection{text, debugInfo}, nil)))
	assert.True(t, hasDWARF(openELF(t, []testSection{text, zdebugInfo}, nil)))
	assert.False(t, hasDWARF(openELF(t, []testSection{text, strippedDebugInfo}, nil)))
	assert.False(t, hasDWARF(openELF(t, []testSection{text}, nil)))
}

func TestBuildID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, testBuildID, buildID(openELF(t, []testSection{text, buildIDSection(t)}, nil)))

	// A file whose notes are in a segment alone.
	assert.Equal(t, testBuildID, buildID(openELF(t, []testSection{text},
		append(note("GNU", 5, []byte{1, 2, 3, 4}), buildIDNote(t, testBuildID)...))))

	assert.Empty(t, buildID(openELF(t, []testSection{text}, nil)))
}

func TestNoteBuildID(t *testing.T) {
	t.Parallel()

	ident := buildIDNote(t, testBuildID)

	// The build ID after a note of another type, and of another owner.
	notes := append(append(note("GNU", 1, []byte{0, 0, 0, 0, 3, 0, 0, 0}),
		note("Go", ntGNUBuildID, []byte("not a GNU build ID"))...), ident...)
	assert.Equal(t, testBuildID, noteBuildID(notes, binary.LittleEndian, 4))

	assert.Empty(t, noteBuildID(ident[:len(ident)-1], binary.LittleEndian, 4), "truncated")
	assert.Empty(t, noteBuildID(nil, binary.LittleEndian, 4))
	assert.Empty(t, noteBuildID(note("GNU", 1, []byte{1}), binary.LittleEndian, 4))
}

func TestCheckDebugInfo(t *testing.T) {
	t.Parallel()

	debugFile := filepath.Join("usr", "lib", "debug", ".build-id", testBuildID[:2],
		testBuildID[2:]+".debug")

	cases := []struct {
		name      string
		tarantool []testSection
		debug     []testSection
		err       string
	}{
		{name: "own", tarantool: []testSection{text, debugInfo}, debug: nil, err: ""},
		{
			name: "debug file", tarantool: []testSection{text, buildIDSection(t)},
			debug: []testSection{strippedDebugInfo, buildIDSection(t), zdebugInfo}, err: "",
		},
		{
			name: "no debug file", tarantool: []testSection{text, buildIDSection(t)},
			debug: nil,
			err: "no debugging information: no .debug_info in the tarantool, and no " +
				debugFile + " of its build ID in the archive",
		},
		{
			name: "stripped debug file", tarantool: []testSection{text, buildIDSection(t)},
			debug: []testSection{strippedDebugInfo, buildIDSection(t)},
			err: "no debugging information: no .debug_info in the tarantool, and no " +
				debugFile + " of its build ID in the archive",
		},
		{
			name: "no build ID", tarantool: []testSection{text}, debug: nil,
			err: "no debugging information: no .debug_info in the tarantool, and no build " +
				"ID to find a debug file by",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeELF(t, filepath.Join(dir, "tarantool"), testCase.tarantool, nil)

			if testCase.debug != nil {
				writeELF(t, filepath.Join(dir, debugFile), testCase.debug, nil)
			}

			err := checkDebugInfo(dir)
			if testCase.err == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, errNoDebugInfo)
			assert.EqualError(t, err, testCase.err)
		})
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tarantool"), []byte("text"), 0o755))

	err := checkDebugInfo(dir)
	require.Error(t, err)
	require.NotErrorIs(t, err, errNoDebugInfo)
}

func TestWarnDebugInfo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeELF(t, filepath.Join(dir, "tarantool"), []testSection{text, buildIDSection(t)}, nil)

	log, recorder := logtest.New(t)
	warnDebugInfo(log, dir)
	assert.Equal(t, []string{
		"the tarantool of the core has no debugging information: no .debug_info in the " +
			"tarantool, and no usr/lib/debug/.build-id/78/" + testBuildID[2:] + ".debug of its " +
			"build ID in the archive: the lj-* commands of LuaJIT and the pretty-printers of " +
			"Tarantool will not work, plain backtraces will; a core packed where the debug " +
			"file of its tarantool is installed has it",
	}, warnings(recorder))

	// A tarantool with debugging information, and one that is no ELF file,
	// get no warning.
	writeELF(t, filepath.Join(dir, "tarantool"), []testSection{text, debugInfo}, nil)

	log, recorder = logtest.New(t)
	warnDebugInfo(log, dir)
	assert.Empty(t, warnings(recorder))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tarantool"), []byte("text"), 0o755))

	log, recorder = logtest.New(t)
	warnDebugInfo(log, dir)
	assert.Empty(t, warnings(recorder))
}
