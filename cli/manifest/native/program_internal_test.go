package native

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// elfSpec describes the ELF file elfProgram generates: its type and the
// segments a loader reads.
type elfSpec struct {
	typ elf.Type
	// interp, when set, is the loader PT_INTERP names.
	interp string
	// dynamic adds a PT_DYNAMIC segment holding needed and flags1.
	dynamic bool
	needed  []string
	flags1  elf.DynFlag1
}

// elfBase is the address elfProgram loads its one PT_LOAD segment at.
const elfBase = 0x400000

// Sizes of the 64-bit ELF structures elfProgram writes.
const (
	ehdrSize   = 64
	phdrSize   = 56
	shdrSize   = 64
	dynEntSize = 16
)

// elfProgram generates a little-endian 64-bit x86-64 ELF file after spec,
// with program headers and no sections, as a stripped program may be, and
// checks that debug/elf reads it back. One PT_LOAD segment maps the whole
// file, so the string table the dynamic segment points at is found through
// it, as the loader finds it.
func elfProgram(t *testing.T, spec elfSpec) []byte {
	t.Helper()

	var phnum uint16 = 1

	if spec.interp != "" {
		phnum++
	}

	if spec.dynamic {
		phnum++
	}

	var interp []byte

	if spec.interp != "" {
		interp = append([]byte(spec.interp), 0)
	}

	strtab := []byte{0}

	var entries [][2]uint64

	for _, name := range spec.needed {
		entries = append(entries, [2]uint64{uint64(elf.DT_NEEDED), uint64(len(strtab))})
		strtab = append(append(strtab, name...), 0)
	}

	interpOff := ehdrSize + phdrSize*uint64(phnum)
	strtabOff := interpOff + uint64(len(interp))

	entries = append(entries, [2]uint64{uint64(elf.DT_STRTAB), elfBase + strtabOff})
	if spec.flags1 != 0 {
		entries = append(entries, [2]uint64{uint64(elf.DT_FLAGS_1), uint64(spec.flags1)})
	}

	entries = append(entries, [2]uint64{uint64(elf.DT_NULL), 0})

	dynOff := (strtabOff + uint64(len(strtab)) + 7) &^ 7
	dynSize := dynEntSize * uint64(len(entries))

	size := dynOff
	if spec.dynamic {
		size += dynSize
	}

	hdr := elf.Header64{
		Type:      uint16(spec.typ),
		Machine:   uint16(elf.EM_X86_64),
		Version:   uint32(elf.EV_CURRENT),
		Entry:     elfBase,
		Phoff:     ehdrSize,
		Ehsize:    ehdrSize,
		Phentsize: phdrSize,
		Phnum:     phnum,
		Shentsize: shdrSize,
	}

	copy(hdr.Ident[:], elf.ELFMAG)

	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)

	progs := []elf.Prog64{{
		Type: uint32(elf.PT_LOAD), Flags: uint32(elf.PF_R | elf.PF_X),
		Vaddr: elfBase, Paddr: elfBase, Filesz: size, Memsz: size, Align: 8,
	}}

	if spec.interp != "" {
		progs = append(progs, elf.Prog64{
			Type: uint32(elf.PT_INTERP), Flags: uint32(elf.PF_R),
			Off: interpOff, Vaddr: elfBase + interpOff,
			Filesz: uint64(len(interp)), Memsz: uint64(len(interp)), Align: 1,
		})
	}

	if spec.dynamic {
		progs = append(progs, elf.Prog64{
			Type: uint32(elf.PT_DYNAMIC), Flags: uint32(elf.PF_R | elf.PF_W),
			Off: dynOff, Vaddr: elfBase + dynOff,
			Filesz: dynSize, Memsz: dynSize, Align: 8,
		})
	}

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, binary.LittleEndian, &hdr))
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, progs))
	buf.Write(interp)
	buf.Write(strtab)

	buf.Write(make([]byte, dynOff-strtabOff-uint64(len(strtab))))

	if spec.dynamic {
		require.NoError(t, binary.Write(&buf, binary.LittleEndian, entries))
	}

	data := buf.Bytes()
	require.Equal(t, size, uint64(len(data)))

	parsed, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	require.Len(t, parsed.Progs, int(phnum))
	require.Equal(t, spec.typ, parsed.Type)

	return data
}

// inspect writes data to a file and inspects it as a program.
func inspect(t *testing.T, data []byte) (Program, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "program")
	require.NoError(t, os.WriteFile(path, data, testFilePerm))

	return InspectProgram(path)
}

func TestInspectProgramELF(t *testing.T) {
	t.Parallel()

	const loader = "/lib64/ld-linux-x86-64.so.2"

	linuxAmd64 := []Platform{{OS: "linux", Arch: "amd64"}}

	tests := []struct {
		name string
		spec elfSpec
		want Program
	}{
		{
			// What go build makes without cgo.
			name: "static executable",
			spec: elfSpec{typ: elf.ET_EXEC},
			want: Program{Platforms: linuxAmd64, Executable: true},
		},
		{
			// What Rust makes for a musl target: a dynamic segment, for its
			// own relocations, but no loader and no library.
			name: "static-pie",
			spec: elfSpec{typ: elf.ET_DYN, dynamic: true, flags1: elf.DF_1_PIE | elf.DF_1_NOW},
			want: Program{Platforms: linuxAmd64, Executable: true},
		},
		{
			name: "dynamic pie",
			spec: elfSpec{
				typ: elf.ET_DYN, interp: loader, dynamic: true,
				needed: []string{"libgcc_s.so.1", "libc.so.6"}, flags1: elf.DF_1_PIE,
			},
			want: Program{
				Platforms: linuxAmd64, Executable: true, Interpreter: loader,
				Needed: []string{"libgcc_s.so.1", "libc.so.6"},
			},
		},
		{
			name: "dynamic executable",
			spec: elfSpec{
				typ: elf.ET_EXEC, interp: loader, dynamic: true, needed: []string{"libc.so.6"},
			},
			want: Program{
				Platforms: linuxAmd64, Executable: true, Interpreter: loader,
				Needed: []string{"libc.so.6"},
			},
		},
		{
			// A loader alone is enough to be dynamic.
			name: "executable with a loader and no library",
			spec: elfSpec{typ: elf.ET_EXEC, interp: loader},
			want: Program{Platforms: linuxAmd64, Executable: true, Interpreter: loader},
		},
		{
			name: "shared library",
			spec: elfSpec{typ: elf.ET_DYN, dynamic: true, needed: []string{"libc.so.6"}},
			want: Program{Platforms: linuxAmd64, Needed: []string{"libc.so.6"}},
		},
		{
			name: "object file",
			spec: elfSpec{typ: elf.ET_REL},
			want: Program{Platforms: linuxAmd64},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prog, err := inspect(t, elfProgram(t, tt.spec))
			require.NoError(t, err)

			if tt.want.Needed == nil {
				assert.Empty(t, prog.Needed)

				prog.Needed = nil
			}

			assert.Equal(t, tt.want, prog)
		})
	}
}

func TestProgramStatic(t *testing.T) {
	t.Parallel()

	assert.True(t, Program{Executable: true}.Static())
	assert.False(t, Program{Interpreter: "/lib/ld-musl-x86_64.so.1"}.Static())
	assert.False(t, Program{Needed: []string{"libc.so.6"}}.Static())
}

func TestProgramStaticPlatforms(t *testing.T) {
	t.Parallel()

	const (
		libSystem      = "/usr/lib/libSystem.B.dylib"
		libResolv      = "/usr/lib/libresolv.9.dylib"
		security       = "/System/Library/Frameworks/Security.framework/Versions/A/Security"
		coreFoundation = "/System/Library/Frameworks/" +
			"CoreFoundation.framework/Versions/A/CoreFoundation"
	)

	tests := []struct {
		name        string
		os          string
		needed      []string
		interpreter string
		static      bool
	}{
		{name: "darwin no libraries", os: "darwin", static: true},
		{name: "darwin libc", os: "darwin", needed: []string{libSystem}, static: true},
		{name: "darwin base", os: "darwin", needed: []string{libSystem, libResolv}, static: true},
		{name: "darwin security", os: "darwin", needed: []string{libSystem, libResolv, security}},
		{name: "darwin framework", os: "darwin", needed: []string{coreFoundation}},
		{name: "darwin loader", os: "darwin", interpreter: "/usr/lib/dyld"},
		{name: "linux no libraries", os: "linux", static: true},
		{name: "linux libc", os: "linux", needed: []string{"libc.so.6"}},
		{name: "linux darwin library", os: "linux", needed: []string{libSystem}},
	}

	for _, tt := range tests {
		for _, arch := range []string{"arm64", "amd64"} {
			t.Run(tt.name+"/"+arch, func(t *testing.T) {
				t.Parallel()

				prog := Program{
					Platforms: []Platform{{OS: tt.os, Arch: arch}},
					Needed:    tt.needed, Interpreter: tt.interpreter,
				}
				assert.Equal(t, tt.static, prog.Static())
				assert.Equal(t, tt.needed, prog.Needed)
			})
		}
	}
}

func TestInspectProgramMachO(t *testing.T) {
	t.Parallel()

	dylib := machoSlice(t, macho.CpuArm64, binary.LittleEndian)
	// The file type follows the magic, the CPU type and its subtype.
	binary.LittleEndian.PutUint32(dylib[12:], uint32(macho.TypeDylib))

	tests := []struct {
		name       string
		data       []byte
		executable bool
	}{
		{"thin executable", machoSlice(t, macho.CpuArm64, binary.LittleEndian), true},
		{"universal executable", fatMacho(t, macho.CpuAmd64, macho.CpuArm64), true},
		{"dylib", dylib, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prog, err := inspect(t, tt.data)
			require.NoError(t, err)
			assert.Equal(t, tt.executable, prog.Executable)
			assert.NotEmpty(t, prog.Platforms)
		})
	}
}

// machoWithDylib returns a thin 64-bit Mach-O executable for cpu that loads
// the dylibs named, and checks that debug/macho reads them back.
func machoWithDylib(t *testing.T, cpu macho.Cpu, libs ...string) []byte {
	t.Helper()

	var cmds bytes.Buffer

	for _, lib := range libs {
		// The name is an offset from the command, past the command itself.
		const (
			loadDylib  = 0xc
			nameOffset = 24
		)

		name := append([]byte(lib), 0)

		size := nameOffset + len(name)

		size += (4 - size%4) % 4

		command := []uint32{loadDylib, uint32(size), nameOffset, 0, 0, 0} //nolint:gosec

		for _, word := range command {
			require.NoError(t, binary.Write(&cmds, binary.LittleEndian, word))
		}

		cmds.Write(name)
		cmds.Write(make([]byte, size-nameOffset-len(name)))
	}

	hdr := macho.FileHeader{
		Magic: macho.Magic64, Cpu: cpu, Type: macho.TypeExec,
		Ncmd:  uint32(len(libs)),  //nolint:gosec // A few dylibs.
		Cmdsz: uint32(cmds.Len()), //nolint:gosec // A small test command list.
	}

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, binary.LittleEndian, &hdr))
	require.NoError(t, binary.Write(&buf, binary.LittleEndian, uint32(0)))
	buf.Write(cmds.Bytes())

	parsed, err := macho.NewFile(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)

	got, err := parsed.ImportedLibraries()
	require.NoError(t, err)
	require.Equal(t, libs, got)

	return buf.Bytes()
}

// TestInspectProgramUniversalNeeded pins that a universal Mach-O asks for the
// libraries of every slice it holds: one dynamic slice makes the file
// dynamic, whatever the order of the slices.
func TestInspectProgramUniversalNeeded(t *testing.T) {
	t.Parallel()

	const (
		libSystem = "/usr/lib/libSystem.B.dylib"
		security  = "/System/Library/Frameworks/Security.framework/Versions/A/Security"
	)

	fat := func(t *testing.T, cpus ...macho.Cpu) []byte {
		t.Helper()

		slices := make([][]byte, 0, len(cpus))
		for _, cpu := range cpus {
			libs := []string{libSystem}
			if cpu == macho.CpuAmd64 {
				libs = append(libs, security)
			}

			slices = append(slices, machoWithDylib(t, cpu, libs...))
		}

		return universalMacho(t, cpus, slices)
	}

	for name, data := range map[string][]byte{
		"static slice first":  fat(t, macho.CpuArm64, macho.CpuAmd64),
		"dynamic slice first": fat(t, macho.CpuAmd64, macho.CpuArm64),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			prog, err := inspect(t, data)
			require.NoError(t, err)
			// The libraries of every slice, sorted and without duplicates.
			assert.Equal(t, []string{security, libSystem}, prog.Needed)
			assert.False(t, prog.Static(), "a slice that needs Security makes the file dynamic")
		})
	}
}

func TestInspectProgramNotNative(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"script": []byte("#!/bin/sh\nexit 0\n"),
		"empty":  {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := inspect(t, data)
			require.ErrorIs(t, err, ErrNotNative)
		})
	}
}

// TestInspectProgramMalformedELF: a file whose header reads as ELF but whose
// program headers are not there is an error, not a program.
func TestInspectProgramMalformedELF(t *testing.T) {
	t.Parallel()

	data := elfProgram(t, elfSpec{typ: elf.ET_EXEC, interp: "/lib/ld.so"})

	_, err := inspect(t, data[:ehdrSize+8])
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotNative)
}

// TestInspectProgramOfRealBinaries runs the inspection on binaries a real
// linker produced: the test binary, and the true of the system, which every
// Linux distribution links dynamically.
func TestInspectProgramOfRealBinaries(t *testing.T) {
	t.Parallel()

	self, err := os.Executable()
	require.NoError(t, err)

	prog, err := InspectProgram(self)
	require.NoError(t, err)
	assert.True(t, prog.Executable)
	assert.Equal(t, []Platform{Host()}, prog.Platforms)

	if runtime.GOOS != "linux" {
		return
	}

	for _, path := range []string{"/bin/true", "/usr/bin/true"} {
		prog, err := InspectProgram(path)
		if err != nil {
			continue
		}

		assert.True(t, prog.Executable, path)
		assert.False(t, prog.Static(), path)
		assert.True(t, strings.HasPrefix(prog.Interpreter, "/"), prog.Interpreter)
		assert.NotEmpty(t, prog.Needed, path)

		return
	}
}
