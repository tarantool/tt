package coredump

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeReturn decodes the code of functions that return the address
// of a string, as tarantool_version and tarantool_package are, for the
// address: the code of the tarantools of Tarantool, by their addresses, as
// objdump disassembles them, and of the other forms of x86-64 and AArch64
// the decoders take, and code they refuse.
func TestDecodeReturn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		machine elf.Machine
		pc      uint64
		code    []byte
		want    uint64
		ok      bool
	}{
		{
			// mov $0xf8b1c7,%eax; ret of the static build of 3.9.0.
			name: "x86-64 static", machine: elf.EM_X86_64, pc: 0x615080,
			code: []byte{0xb8, 0xc7, 0xb1, 0xf8, 0x00, 0xc3}, want: 0xf8b1c7, ok: true,
		},
		{
			// endbr64; lea 0x2f5705(%rip),%rax; ret of a PIE of 3.10.0.
			name: "x86-64 PIE", machine: elf.EM_X86_64, pc: 0x240f80,
			code: []byte{
				0xf3, 0x0f, 0x1e, 0xfa, 0x48, 0x8d, 0x05, 0x05, 0x57, 0x2f, 0x00, 0xc3,
			},
			want: 0x536690, ok: true,
		},
		{
			// The same of Tarantool Enterprise 3.7.0.
			name: "x86-64 Enterprise", machine: elf.EM_X86_64, pc: 0x4360f0,
			code: []byte{
				0xf3, 0x0f, 0x1e, 0xfa, 0x48, 0x8d, 0x05, 0x1e, 0xe0, 0x74, 0x01, 0xc3,
			},
			want: 0x1b84119, ok: true,
		},
		{
			// push %rbp; mov %rsp,%rbp; lea -0x10(%rip),%rax; pop %rbp; ret of -O0.
			name: "x86-64 frame", machine: elf.EM_X86_64, pc: 0x1000,
			code: []byte{
				0x55, 0x48, 0x89, 0xe5, 0x48, 0x8d, 0x05, 0xf0, 0xff, 0xff, 0xff, 0x5d, 0xc3,
			},
			want: 0x1000 + 11 - 0x10, ok: true,
		},
		{
			// mov $0x402010,%rax; repz ret.
			name: "x86-64 mov rax", machine: elf.EM_X86_64, pc: 0x401000,
			code: []byte{0x48, 0xc7, 0xc0, 0x10, 0x20, 0x40, 0x00, 0xf3, 0xc3}, want: 0x402010,
			ok: true,
		},
		{
			// adrp x0,0xe13000; add x0,x0,#0x890; ret of the static build of
			// 3.9.0 for AArch64.
			name: "aarch64 static", machine: elf.EM_AARCH64, pc: 0x5d7938,
			code: []byte{0xe0, 0x41, 0x00, 0x90, 0x00, 0x40, 0x22, 0x91, 0xc0, 0x03, 0x5f, 0xd6},
			want: 0xe13890, ok: true,
		},
		{
			// adrp x0,0xc3c000; add x0,x0,#0xe00; ret of 3.0.2, immlo of 3.
			name: "aarch64 immlo", machine: elf.EM_AARCH64, pc: 0x5b5da0,
			code: []byte{0x20, 0x34, 0x00, 0xf0, 0x00, 0x00, 0x38, 0x91, 0xc0, 0x03, 0x5f, 0xd6},
			want: 0xc3ce00, ok: true,
		},
		{
			// bti c; paciasp; adrp x0,.-0x10 pages; add x0,x0,#0x1,lsl #12;
			// autiasp; ret.
			name: "aarch64 branch protection", machine: elf.EM_AARCH64, pc: 0x2000000,
			code: arm64Code(0xd503245f, 0xd503233f, 0x90ffff80, 0x91400400, 0xd50323bf,
				0xd65f03c0),
			want: 0x2000000 - 0x10*0x1000 + 0x1000, ok: true,
		},
		{
			// adr x0, .-4; ret.
			name: "aarch64 adr", machine: elf.EM_AARCH64, pc: 0x1000,
			code: arm64Code(0x10ffffe0, 0xd65f03c0), want: 0x1000 - 4, ok: true,
		},
		{
			name: "x86-64 other register", machine: elf.EM_X86_64, pc: 0x1000,
			code: []byte{0x48, 0x8d, 0x0d, 0x10, 0x00, 0x00, 0x00, 0xc3}, want: 0, ok: false,
		},
		{
			name: "x86-64 no ret", machine: elf.EM_X86_64, pc: 0x1000,
			code: []byte{0xb8, 0x10, 0x20, 0x40, 0x00, 0x90}, want: 0, ok: false,
		},
		{
			name: "x86-64 frame not popped", machine: elf.EM_X86_64, pc: 0x1000,
			code: []byte{0x55, 0x48, 0x89, 0xe5, 0xb8, 0x10, 0x20, 0x40, 0x00, 0xc3}, want: 0,
			ok: false,
		},
		{
			name: "x86-64 short", machine: elf.EM_X86_64, pc: 0x1000,
			code: []byte{0xb8, 0x10, 0x20}, want: 0, ok: false,
		},
		{
			name: "x86-64 nops", machine: elf.EM_X86_64, pc: 0x1000,
			code: []byte{0x90, 0x90, 0x90, 0xc3}, want: 0, ok: false,
		},
		{
			// adrp x1, then add x0, x1.
			name: "aarch64 other register", machine: elf.EM_AARCH64, pc: 0x1000,
			code: arm64Code(0x90000001, 0x91000020, 0xd65f03c0), want: 0, ok: false,
		},
		{
			name: "aarch64 no ret", machine: elf.EM_AARCH64, pc: 0x1000,
			code: arm64Code(0x90000000, 0x91000000, 0xd503201f), want: 0, ok: false,
		},
		{
			name: "aarch64 adrp alone", machine: elf.EM_AARCH64, pc: 0x1000,
			code: arm64Code(0x90000000, 0xd65f03c0), want: 0, ok: false,
		},
		{
			name: "aarch64 empty", machine: elf.EM_AARCH64, pc: 0x1000, code: nil, want: 0,
			ok: false,
		},
	}

	for _, testCase := range cases {
		decode := decodeX86Return
		if testCase.machine == elf.EM_AARCH64 {
			decode = decodeARM64Return
		}

		got, ok := decode(testCase.pc, testCase.code)
		assert.Equal(t, testCase.ok, ok, testCase.name)
		assert.Equal(t, testCase.want, got, "%s: %#x, not %#x", testCase.name, got,
			testCase.want)
	}
}

// arm64Code returns the code of the AArch64 instructions words.
func arm64Code(words ...uint32) []byte {
	code := make([]byte, 0, 4*len(words))

	for _, word := range words {
		code = binary.LittleEndian.AppendUint32(code, word)
	}

	return code
}

// versionSection is a section of a versionELF file, at addr of the memory of
// a process, of flags, read-only data when they are 0.
type versionSection struct {
	name  string
	addr  uint64
	data  []byte
	flags elf.SectionFlag
}

// versionFunction is a function of .text of a versionELF file.
type versionFunction struct {
	name string
	addr uint64
}

// stringsAt returns the data of a section of size bytes with the C strings
// of texts at their offsets.
func stringsAt(size int, texts map[int]string) []byte {
	data := make([]byte, size)

	for offset, text := range texts {
		copy(data[offset:], text+"\x00")
	}

	return data
}

// versionELF returns a test ELF file, of 64 bits and little-endian, of
// machine: the sections, each in a PT_LOAD of its own, and the functions,
// of .text, in its symbol table.
//
//nolint:gosec // The sizes are those of test data.
func versionELF(t *testing.T, machine elf.Machine, sections []versionSection,
	functions []versionFunction,
) *elf.File {
	t.Helper()

	const (
		headerSize = 64
		progSize   = 56
		symSize    = 24
		sectSize   = 64
	)

	textIndex := -1

	for index, section := range sections {
		if section.name == ".text" {
			textIndex = index + 1
		}
	}

	require.Positive(t, textIndex, "a versionELF file has .text")

	strtab := []byte{0}

	var symtab bytes.Buffer

	symtab.Write(make([]byte, symSize))

	for _, function := range functions {
		name := uint32(len(strtab))

		strtab = append(strtab, function.name+"\x00"...)

		require.NoError(t, binary.Write(&symtab, binary.LittleEndian, elf.Sym64{
			Name: name, Info: elf.ST_INFO(elf.STB_GLOBAL, elf.STT_FUNC), Other: 0,
			Shndx: uint16(textIndex), Value: function.addr, Size: 0,
		}))
	}

	all := append([]versionSection(nil), sections...)

	all = append(all,
		versionSection{name: ".strtab", addr: 0, data: strtab, flags: 0},
		versionSection{name: ".symtab", addr: 0, data: symtab.Bytes(), flags: 0},
	)

	shstrtab := []byte{0}
	for _, section := range all {
		shstrtab = append(shstrtab, section.name+"\x00"...)
	}

	shstrtab = append(shstrtab, ".shstrtab\x00"...)
	all = append(all, versionSection{name: ".shstrtab", addr: 0, data: shstrtab, flags: 0})

	offset := uint64(headerSize + progSize*len(sections))
	headers := make([]elf.Section64, 1, 1+len(all))
	progs := make([]elf.Prog64, 0, len(sections))
	nameOffset := uint32(1)

	var content bytes.Buffer

	for index, section := range all {
		header := elf.Section64{
			Name: nameOffset, Type: uint32(elf.SHT_PROGBITS), Flags: 0, Addr: section.addr,
			Off: offset, Size: uint64(len(section.data)), Link: 0, Info: 0, Addralign: 1,
			Entsize: 0,
		}

		nameOffset += uint32(len(section.name) + 1)

		switch section.name {
		case ".strtab", ".shstrtab":
			header.Type = uint32(elf.SHT_STRTAB)
		case ".symtab":
			header.Type, header.Link, header.Info = uint32(elf.SHT_SYMTAB), uint32(index), 1
			header.Entsize = symSize
		default:
			flags := section.flags
			if flags == 0 {
				flags = elf.SHF_ALLOC
			}

			header.Flags = uint64(flags)
			progs = append(progs, elf.Prog64{
				Type: uint32(elf.PT_LOAD), Flags: uint32(elf.PF_R | elf.PF_X), Off: offset,
				Vaddr: section.addr, Paddr: section.addr, Filesz: header.Size,
				Memsz: header.Size, Align: 1,
			})
		}

		headers = append(headers, header)

		offset += header.Size

		content.Write(section.data)
	}

	var file bytes.Buffer

	require.NoError(t, binary.Write(&file, binary.LittleEndian, elf.Header64{
		Ident: [16]byte{
			0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB),
			byte(elf.EV_CURRENT),
		},
		Type: uint16(elf.ET_EXEC), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT),
		Entry: 0, Phoff: headerSize, Shoff: offset, Flags: 0, Ehsize: headerSize,
		Phentsize: progSize, Phnum: uint16(len(progs)), Shentsize: sectSize,
		Shnum: uint16(len(headers)), Shstrndx: uint16(len(headers) - 1),
	}))
	require.NoError(t, binary.Write(&file, binary.LittleEndian, progs))
	file.Write(content.Bytes())
	require.NoError(t, binary.Write(&file, binary.LittleEndian, headers))

	parsed, err := elf.NewFile(bytes.NewReader(file.Bytes()))
	require.NoError(t, err)

	return parsed
}

// textAt returns .text at addr with the code of functions at their
// offsets, nops elsewhere.
func textAt(addr uint64, size int, code map[int][]byte) versionSection {
	data := bytes.Repeat([]byte{0x90}, size)

	for offset, bytes := range code {
		copy(data[offset:], bytes)
	}

	return versionSection{
		name: ".text", addr: addr, data: data, flags: elf.SHF_ALLOC | elf.SHF_EXECINSTR,
	}
}

// TestReadBinaryVersion reads the version of a tarantool from its file, as
// the first line of tarantool --version prints it: of the strings its
// tarantool_version and tarantool_package return, by the decoded code,
// laid out as in the tarantools of Tarantool; of the only version string of
// its read-only data when the code cannot be decoded, or returns another
// string; Tarantool Enterprise by the string of its read-only data without
// tarantool_package. A tarantool of no version string, or of two, is an
// error that says why of both ways.
func TestReadBinaryVersion(t *testing.T) {
	t.Parallel()

	nops := []byte{0x90, 0x90, 0x90, 0xc3}

	cases := []struct {
		name      string
		machine   elf.Machine
		sections  []versionSection
		functions []versionFunction
		want      string
		err       []string
	}{
		{
			name: "x86-64 static", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x615070, 0x20, map[int][]byte{
					0x00: {0xb8, 0xa8, 0xbb, 0xf8, 0x00, 0xc3},
					0x10: {0xb8, 0xc7, 0xb1, 0xf8, 0x00, 0xc3},
				}),
				{
					name: ".rodata", addr: 0xf8b000, flags: 0,
					data: stringsAt(0x1000, map[int]string{
						0x1c7: "3.9.0-0-g8b9a0f59a95b", 0xba8: "Tarantool",
						0x400: "3.8.0-0-g0123456789",
					}),
				},
			},
			functions: []versionFunction{
				{name: tarantoolPackageSymbol, addr: 0x615070},
				{name: tarantoolSignature, addr: 0x615080},
			},
			want: "Tarantool 3.9.0-0-g8b9a0f59a95b",
		},
		{
			name: "x86-64 Enterprise", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x4360f0, 0x10, map[int][]byte{0: {
					0xf3, 0x0f, 0x1e, 0xfa, 0x48, 0x8d, 0x05, 0x1e, 0xe0, 0x74, 0x01, 0xc3,
				}}),
				{
					name: ".rodata", addr: 0x1b84000, flags: 0,
					data: stringsAt(0x200, map[int]string{
						0x119: "3.7.0-0-g1f1ec9fdf", 0x12c: "Tarantool Enterprise",
						0x150: "Tarantool",
					}),
				},
			},
			functions: []versionFunction{{name: tarantoolSignature, addr: 0x4360f0}},
			want:      "Tarantool Enterprise 3.7.0-0-g1f1ec9fdf",
		},
		{
			name: "x86-64 package of Enterprise", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x401000, 0x20, map[int][]byte{
					0x00: {0xb8, 0x00, 0x20, 0x40, 0x00, 0xc3},
					0x10: {0xb8, 0x40, 0x20, 0x40, 0x00, 0xc3},
				}),
				{
					name: ".rodata", addr: 0x402000, flags: 0,
					data: stringsAt(0x100, map[int]string{
						0x00: "Tarantool Enterprise", 0x40: "2.11.1-0-g96877bd-r589",
					}),
				},
			},
			functions: []versionFunction{
				{name: tarantoolPackageSymbol, addr: 0x401000},
				{name: tarantoolSignature, addr: 0x401010},
			},
			want: "Tarantool Enterprise 2.11.1-0-g96877bd-r589",
		},
		{
			name: "aarch64 static", machine: elf.EM_AARCH64,
			sections: []versionSection{
				textAt(0x5d7928, 0x20, map[int][]byte{
					0x00: {0xe0, 0x41, 0x00, 0xb0, 0x00, 0x60, 0x09, 0x91, 0xc0, 0x03, 0x5f, 0xd6},
					0x10: {0xe0, 0x41, 0x00, 0x90, 0x00, 0x40, 0x22, 0x91, 0xc0, 0x03, 0x5f, 0xd6},
				}),
				{
					name: ".rodata", addr: 0xe13000, flags: 0,
					data: stringsAt(0x2000, map[int]string{
						0x890: "3.9.0-0-g8b9a0f59a9", 0x1258: "Tarantool",
					}),
				},
			},
			functions: []versionFunction{
				{name: tarantoolPackageSymbol, addr: 0x5d7928},
				{name: tarantoolSignature, addr: 0x5d7938},
			},
			want: "Tarantool 3.9.0-0-g8b9a0f59a9",
		},
		{
			name: "not decoded", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x401000, 0x10, map[int][]byte{0: nops}),
				{
					name: ".rodata", addr: 0x402000, flags: 0,
					data: stringsAt(0x100, map[int]string{
						0x00: "3.10.0", 0x10: "3.10.0-entrypoint-36-gdf2e3013e1",
						0x40: "LuaJIT 2.1.0-beta3", 0x60: "3.10.0-entrypoint-36-gdf2e3013e1",
						0x90: "Tarantool 3.9.0-0-g8b9a0f59a95b", 0xb0: "Only Tarantool Enterprise",
					}),
				},
			},
			functions: []versionFunction{{name: tarantoolSignature, addr: 0x401000}},
			want:      "Tarantool 3.10.0-entrypoint-36-gdf2e3013e1",
		},
		{
			name: "another string", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x401000, 0x10, map[int][]byte{0: {0xb8, 0x00, 0x20, 0x40, 0x00, 0xc3}}),
				{
					name: ".rodata", addr: 0x402000, flags: 0,
					data: stringsAt(0x100, map[int]string{
						0x00: "not a version", 0x20: "3.9.0-0-g8b9a0f5-dirty",
					}),
				},
			},
			functions: []versionFunction{{name: tarantoolSignature, addr: 0x401000}},
			want:      "Tarantool 3.9.0-0-g8b9a0f5-dirty",
		},
		{
			name: "another machine", machine: elf.EM_RISCV,
			sections: []versionSection{
				textAt(0x10000, 0x10, nil),
				{
					name: ".rodata", addr: 0x20000, flags: 0,
					data: stringsAt(0x100, map[int]string{0x10: "3.9.0-0-g8b9a0f59a95b"}),
				},
			},
			functions: []versionFunction{{name: tarantoolSignature, addr: 0x10000}},
			want:      "Tarantool 3.9.0-0-g8b9a0f59a95b",
		},
		{
			// A version string of writable data or of the code is not taken.
			name: "no version", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x401000, 0x10, map[int][]byte{0: nops}),
				{
					name: ".data", addr: 0x403000, flags: elf.SHF_ALLOC | elf.SHF_WRITE,
					data: stringsAt(0x40, map[int]string{0x10: "3.9.0-0-g8b9a0f59a95b"}),
				},
				{
					name: ".rodata", addr: 0x402000, flags: 0,
					data: stringsAt(0x100, map[int]string{
						0x00: "Tarantool", 0x10: "3.9.0", 0x20: "Only Tarantool Enterprise",
					}),
				},
			},
			functions: []versionFunction{{name: tarantoolSignature, addr: 0x401000}},
			err: []string{
				"cannot decode the function tarantool_version: its code, 90 90 90 c3 ",
				"returns no address of a string; no version string of the form x.y.z-N-gCOMMIT " +
					"in the read-only data",
			},
		},
		{
			name: "two versions", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x401000, 0x10, map[int][]byte{0: nops}),
				{
					name: ".rodata", addr: 0x402000, flags: 0,
					data: stringsAt(0x100, map[int]string{
						0x00: "3.9.0-0-g8b9a0f59a95b", 0x40: "3.8.0-0-g0123456789",
					}),
				},
			},
			functions: []versionFunction{{name: tarantoolSignature, addr: 0x401000}},
			err: []string{
				"no version string alone in the read-only data: 2 of the form x.y.z-N-gCOMMIT, ",
			},
		},
		{
			name: "an address of nothing", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x401000, 0x10, map[int][]byte{0: {0xb8, 0x00, 0x00, 0x60, 0x00, 0xc3}}),
			},
			functions: []versionFunction{{name: tarantoolSignature, addr: 0x401000}},
			err: []string{
				"no string at 0x600000: no segment of the file has it",
				"no version string of the form x.y.z-N-gCOMMIT in the read-only data",
			},
		},
		{
			name: "no signature", machine: elf.EM_X86_64,
			sections: []versionSection{
				textAt(0x401000, 0x10, nil),
				{
					name: ".rodata", addr: 0x402000, flags: 0,
					data: stringsAt(0x100, map[int]string{0x10: "3.9.0-0-g8b9a0f59a95b"}),
				},
			},
			functions: nil,
			want:      "Tarantool 3.9.0-0-g8b9a0f59a95b",
		},
	}

	for _, testCase := range cases {
		file := versionELF(t, testCase.machine, testCase.sections, testCase.functions)

		got, err := readBinaryVersion(file)
		if testCase.err == nil {
			require.NoError(t, err, testCase.name)
			assert.Equal(t, testCase.want, got, testCase.name)

			continue
		}

		require.ErrorIs(t, err, errNoVersionString, testCase.name)

		for _, part := range testCase.err {
			assert.Contains(t, err.Error(), part, testCase.name)
		}
	}
}

// TestReturnedStringOfCC decodes tarantool_version and tarantool_package
// of a tarantool cc built, static and position-independent, of -O0, of -O2
// and with the frame pointer: the strings they return are read from the
// code of the functions, not found in the data.
func TestReturnedStringOfCC(t *testing.T) {
	t.Parallel()

	variants := [][]string{
		{"-O2", "-no-pie"},
		{"-O2", "-fPIE", "-pie"},
		{"-O0", "-no-pie"},
		{"-O0", "-fPIE", "-pie"},
		{"-O2", "-fno-omit-frame-pointer", "-fPIE", "-pie"},
	}

	for _, flags := range variants {
		name := strings.Join(flags, " ")
		tarantool := compileC(t, t.TempDir(), versionedTarantool,
			append(flags, `-DMARKER="/nonexistent/ran"`)...)

		file, err := elf.Open(tarantool)
		require.NoError(t, err, name)

		t.Cleanup(func() { _ = file.Close() })

		version, err := returnedString(file, tarantoolSignature)
		require.NoError(t, err, name)
		assert.Equal(t, "3.9.0-0-g8b9a0f59a95b", version, name)

		edition, err := returnedString(file, tarantoolPackageSymbol)
		require.NoError(t, err, name)
		assert.Equal(t, "Tarantool", edition, name)
	}
}

// testTarantoolsEnv names the tarantools TestReadBinaryVersionOfTarantools
// reads the versions of, besides the tarantool of PATH: their paths,
// separated as in PATH.
const testTarantoolsEnv = "TT_COREDUMP_TEST_TARANTOOLS"

// TestReadBinaryVersionOfTarantools reads the versions of the tarantools of
// Tarantool there are: the tarantool of PATH and those of
// TT_COREDUMP_TEST_TARANTOOLS. The version of each is the string its
// tarantool_version returns, decoded, not found in its data, with the
// edition its tarantool_package returns, decoded too when its symbols list
// it, and, of one that runs here, the first line of its --version. One of
// no signature, which pack refuses, is passed over. The tarantool of PATH
// is the first one there, as pack takes it, and one that is no ELF file, as
// a tarantool of macOS is a Mach-O, is passed over with a line of the log,
// as pack refuses it, "not an ELF file", among the candidates for the
// tarantool of a core and tries the next one; a tarantool behind it in PATH
// is not looked for, as pack does not, and is checked when listed in
// TT_COREDUMP_TEST_TARANTOOLS. A file listed there that is no ELF file
// fails the test, as pack fails of one given with -e: it was asked for.
// The test is skipped without an ELF tarantool.
func TestReadBinaryVersionOfTarantools(t *testing.T) {
	t.Parallel()

	var paths []string

	path, err := exec.LookPath(tarantoolName)
	if err == nil && hasELFMagic(t, path) {
		paths = append(paths, path)
	} else if err == nil {
		t.Logf("%s, %s: %v, which pack passes over for the tarantool of a core", path,
			pathOrigin, errNotELF)
	}

	for _, listed := range filepath.SplitList(os.Getenv(testTarantoolsEnv)) {
		if listed != "" {
			paths = append(paths, listed)
		}
	}

	if len(paths) == 0 {
		t.Skip("no ELF tarantool in PATH or " + testTarantoolsEnv)
	}

	for _, path := range paths {
		file, err := elf.Open(path)
		require.NoError(t, err, path)

		if !hasSignature(file) {
			_ = file.Close()

			t.Logf("%s: no signature, not a tarantool pack takes", path)

			continue
		}

		_, err = returnedString(file, tarantoolSignature)
		require.NoError(t, err, "%s: tarantool_version is decoded", path)

		_, listed := textSymbol(file, tarantoolPackageSymbol)
		if listed {
			_, err = returnedString(file, tarantoolPackageSymbol)
			require.NoError(t, err, "%s: tarantool_package is decoded", path)
		}

		got, err := readBinaryVersion(file)
		require.NoError(t, err, path)

		_ = file.Close()

		version, ok := parseVersion(got)
		assert.True(t, ok, "%s: %q", path, got)

		shown, err := shownVersion(t.Context(), path)
		if err != nil {
			t.Logf("%s: %s, read %q; it does not run here: %v", path, file.Machine, got, err)

			continue
		}

		assert.Equal(t, shown, got, path)
		t.Logf("%s: %s, %q, release %s, commit %s", path, file.Machine, got, version.Release,
			version.Commit)
	}
}

// hasELFMagic reports whether the file at path begins with the magic
// number of ELF, which elf.Open checks first; a file shorter than it is
// not an ELF file.
func hasELFMagic(t *testing.T, path string) bool {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err, path)

	defer func() { _ = file.Close() }()

	magic := make([]byte, len(elf.ELFMAG))

	_, err = io.ReadFull(file, magic)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}

	require.NoError(t, err, path)

	return string(magic) == elf.ELFMAG
}

// shownVersion returns the first line of tarantool --version of the
// tarantool at path, which the test runs as its user.
func shownVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	//nolint:gosec // G702: the tarantools of the test, of PATH and of its environment.
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", err
	}

	line, _, _ := strings.Cut(string(out), "\n")

	return line, nil
}
