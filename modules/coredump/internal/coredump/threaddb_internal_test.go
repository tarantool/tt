package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// versionedLibrary is a test shared library of glibc: the versions it
// defines, those it needs of each library it needs, weak or not, and its
// banner.
type versionedLibrary struct {
	defines []string
	needs   map[string][]string
	weak    map[string][]string
	banner  string
}

// verdef and verdaux are Elf64_Verdef and Elf64_Verdaux, a version
// definition and its name.
type (
	verdef struct {
		Version, Flags, Ndx, Cnt uint16
		Hash, Aux, Next          uint32
	}
	verdaux struct{ Name, Next uint32 }
)

// verneed and vernaux are Elf64_Verneed and Elf64_Vernaux, a library needed
// and a version needed of it.
type (
	verneed struct {
		Version, Cnt    uint16
		File, Aux, Next uint32
	}
	vernaux struct {
		Hash         uint32
		Flags, Other uint16
		Name, Next   uint32
	}
)

// write writes the library to path as an ELF file of x86_64 with the
// sections its versions are read from, as the dynamic loader reads them:
// .dynstr, .dynsym of the null symbol alone, .gnu.version, .gnu.version_d
// and .gnu.version_r, and .rodata with the banner.
//
//nolint:gosec // The sizes and the indexes are those of test data.
func (l versionedLibrary) write(t *testing.T, path string) string {
	t.Helper()

	strtab := []byte{0}
	str := func(text string) uint32 {
		offset := len(strtab)

		strtab = append(strtab, text+"\x00"...)

		return uint32(offset)
	}

	var definitions, needs bytes.Buffer

	for position, name := range l.defines {
		next := uint32(20 + 8)
		if position == len(l.defines)-1 {
			next = 0
		}

		require.NoError(t, binary.Write(&definitions, binary.LittleEndian, verdef{
			Version: 1, Flags: 0, Ndx: uint16(position + 2), Cnt: 1, Hash: 0, Aux: 20, Next: next,
		}))
		require.NoError(t, binary.Write(&definitions, binary.LittleEndian,
			verdaux{Name: str(name), Next: 0}))
	}

	libs := make([]string, 0, len(l.needs)+len(l.weak))
	for lib := range l.needs {
		libs = append(libs, lib)
	}

	for lib := range l.weak {
		if !slices.Contains(libs, lib) {
			libs = append(libs, lib)
		}
	}

	slices.Sort(libs)

	index := uint16(len(l.defines) + 2)

	for position, lib := range libs {
		type dep struct {
			name  string
			flags uint16
		}

		deps := make([]dep, 0, len(l.needs[lib])+len(l.weak[lib]))

		for _, name := range l.needs[lib] {
			deps = append(deps, dep{name, 0})
		}

		for _, name := range l.weak[lib] {
			deps = append(deps, dep{name, uint16(elf.VER_FLG_WEAK)})
		}

		next := uint32(16 + 16*len(deps))
		if position == len(libs)-1 {
			next = 0
		}

		require.NoError(t, binary.Write(&needs, binary.LittleEndian, verneed{
			Version: 1, Cnt: uint16(len(deps)), File: str(lib), Aux: 16, Next: next,
		}))

		for j, dep := range deps {
			auxNext := uint32(16)
			if j == len(deps)-1 {
				auxNext = 0
			}

			require.NoError(t, binary.Write(&needs, binary.LittleEndian, vernaux{
				Hash: 0, Flags: dep.flags, Other: index, Name: str(dep.name), Next: auxNext,
			}))

			index++
		}
	}

	writeELF(t, path, []testSection{
		{name: ".dynstr", typ: elf.SHT_STRTAB, data: strtab, align: 1},
		{
			name: ".dynsym", typ: elf.SHT_DYNSYM, data: make([]byte, 24), align: 8, link: 1,
			entsize: 24,
		},
		{name: ".gnu.version", typ: elf.SHT_GNU_VERSYM, data: make([]byte, 2), align: 2, link: 2},
		{
			name: ".gnu.version_d", typ: elf.SHT_GNU_VERDEF, data: definitions.Bytes(), align: 8,
			link: 1, info: uint32(len(l.defines)),
		},
		{
			name: ".gnu.version_r", typ: elf.SHT_GNU_VERNEED, data: needs.Bytes(), align: 8,
			link: 1, info: uint32(len(libs)),
		},
		{name: ".rodata", typ: elf.SHT_PROGBITS, data: []byte(l.banner), align: 1},
	}, nil)

	return path
}

// banner returns the banner of glibc of the version.
func banner(version string) string {
	return "GNU C Library (GNU libc) stable release version " + version + ".\n"
}

// The versions of the test libraries of glibc: of the libc of glibc 2.39 of
// Ubuntu 24.04, which defines no GLIBC_ABI_DT_X86_64_PLT, of 2.41 of Debian
// 13 and of 2.43, which do; the versions the libthread_db of 2.43 needs of
// libc.so.6, and those that of 2.41 does.
var (
	glibc239 = []string{"GLIBC_2.2.5", "GLIBC_2.3.4", "GLIBC_2.4", "GLIBC_2.36", "GLIBC_2.39",
		"GLIBC_PRIVATE"}
	glibc241 = append(slices.Clone(glibc239), "GLIBC_2.41", "GLIBC_ABI_DT_X86_64_PLT")
	glibc243 = append(slices.Clone(glibc241), "GLIBC_2.42", "GLIBC_2.43")

	threadDB243 = []string{"GLIBC_ABI_DT_X86_64_PLT", "GLIBC_2.3.4", "GLIBC_2.4", "GLIBC_2.2.5"}
	threadDB241 = []string{"GLIBC_2.36", "GLIBC_2.3.4", "GLIBC_2.4", "GLIBC_2.2.5"}
)

// libc returns the test libc of the version definitions defines and of the
// banner of version.
func libc(defines []string, version string) versionedLibrary {
	return versionedLibrary{defines: defines, needs: nil, weak: nil, banner: banner(version)}
}

// threadDB returns the test libthread_db that needs the versions needs of
// libc.so.6.
func threadDB(needs []string) versionedLibrary {
	return versionedLibrary{
		defines: []string{"GLIBC_2.2.5"}, needs: map[string][]string{libcName: needs}, weak: nil,
		banner: "",
	}
}

func TestTLSProblem(t *testing.T) {
	t.Parallel()

	type facts = threadDBFacts

	var (
		r390, r370, r364 = release{3, 9, 0}, release{3, 7, 0}, release{3, 6, 4}
		gdb15, gdb163    = debuggerVersion{15, 1}, debuggerVersion{16, 3}
		gdb17            = debuggerVersion{17, 1}
		snapshot17       = debuggerVersion{17, 0, 50}
		g243, g239, g244 = glibcVersion{2, 43}, glibcVersion{2, 39}, glibcVersion{2, 44}
		g241, g242       = glibcVersion{2, 41}, glibcVersion{2, 42}
		amd64, arm64     = elf.EM_X86_64, elf.EM_AARCH64
		noLoad           = "it needs GLIBC_ABI_DT_X86_64_PLT, which /lib/libc.so.6 does not " +
			"define"
		otherMachine = "it is a library of EM_AARCH64, this host is of EM_X86_64"
		zeroed       = "libthread_db fails on the stacks of zeros of this core"
		instead243   = "or inspect the core on a host of glibc 2.43"
	)

	cases := []struct {
		name                      string
		facts                     facts
		problem, instead, unknown string
	}{
		{
			"no version", facts{GDB: gdb15, ThreadDB: true, CoreGlibc: g243, HostGlibc: g239},
			"", "", "the version of the core",
		},
		{
			"before 3.7", facts{Release: r364, GDB: gdb15, CoreGlibc: g243, HostGlibc: g239},
			"", "", "",
		},
		{
			"no gdb version", facts{Release: r390, CoreGlibc: g243, HostGlibc: g239},
			"", "", "the version of gdb",
		},
		{
			"gdb 16", facts{Release: r370, GDB: gdb163, CoreGlibc: g243, HostGlibc: g239},
			"the archive has no libthread_db, and that of this host is of glibc 2.39, the core " +
				"of 2.43", instead243, "",
		},
		{"gdb 17", facts{Release: r390, GDB: gdb17, CoreGlibc: g243, HostGlibc: g239}, "", "", ""},
		{
			// A snapshot of 17 may predate the TLS support of gdb.
			"a snapshot of gdb 17", facts{
				Release: r390, GDB: snapshot17, CoreGlibc: g243, HostGlibc: g239,
			},
			"the archive has no libthread_db, and that of this host is of glibc 2.39, the core " +
				"of 2.43", instead243, "",
		},
		// The symbol tells whether tarantool_L is thread-local before the
		// version does: a development build of 3.7 may have it either way.
		{
			"a thread-local symbol", facts{
				Release: r364, LuaState: luaStateThreadLocal, GDB: gdb15, CoreGlibc: g243,
				HostGlibc: g239,
			},
			"the archive has no libthread_db, and that of this host is of glibc 2.39, the core " +
				"of 2.43", instead243, "",
		},
		{
			"a thread-local symbol, no version", facts{
				LuaState: luaStateThreadLocal, GDB: gdb15, CoreGlibc: g243, HostGlibc: g239,
			},
			"the archive has no libthread_db, and that of this host is of glibc 2.39, the core " +
				"of 2.43", instead243, "",
		},
		{
			"a symbol of the process", facts{
				Release: r390, LuaState: luaStateGlobal, GDB: gdb15, CoreGlibc: g243,
				HostGlibc: g239,
			}, "", "", "",
		},
		{
			"a thread db that loads", facts{
				Release: r390, GDB: gdb15, ThreadDB: true, CoreGlibc: g243, HostGlibc: g239,
			}, "", "", "",
		},
		// libthread_db fails on the stacks of zeros, whichever loads.
		{
			"stacks of zeros, a thread db that loads", facts{
				Release: r390, GDB: gdb163, ZeroedStacks: 1, ThreadDB: true, CoreGlibc: g243,
				HostGlibc: g241,
			}, zeroed, zeroedStacksInstead, "",
		},
		{
			"stacks of zeros, the host glibc", facts{
				Release: r390, GDB: gdb163, ZeroedStacks: 2, CoreGlibc: g242, HostGlibc: g242,
			}, zeroed, zeroedStacksInstead, "",
		},
		{
			"stacks of zeros, gdb 17", facts{
				Release: r390, GDB: gdb17, ZeroedStacks: 1, ThreadDB: true, CoreGlibc: g243,
				HostGlibc: g241,
			}, "", "", "",
		},
		{
			"stacks of zeros, before 3.7", facts{
				Release: r364, GDB: gdb163, ZeroedStacks: 1, CoreGlibc: g243, HostGlibc: g243,
			}, "", "", "",
		},
		{
			"a thread db that does not load", facts{
				Release: r370, GDB: gdb15, ThreadDB: true, NoLoad: noLoad, CoreGlibc: g243,
				HostGlibc: g239,
			},
			"the libthread_db of the archive does not load here, " + noLoad + ", and that of " +
				"this host is of glibc 2.39, the core of 2.43", instead243 + " or later", "",
		},
		// Neither the libthread_db of the archive nor that of this host
		// serves a core of another machine, whatever the glibc.
		{
			"a thread db of another machine", facts{
				Release: r390, GDB: gdb15, ThreadDB: true, NoLoad: otherMachine,
				CoreMachine: arm64, HostMachine: amd64, CoreGlibc: g243, HostGlibc: g243,
			},
			"the libthread_db of the archive does not load here, " + otherMachine + ", and " +
				"that of this host is of EM_X86_64, the core of EM_AARCH64",
			"or inspect the core on a host of EM_AARCH64 and glibc 2.43 or later", "",
		},
		{
			"no thread db, another machine", facts{
				Release: r390, GDB: gdb15, CoreMachine: arm64, HostMachine: amd64,
				CoreGlibc: g243, HostGlibcUnknown: "no libc.so.6 under /lib",
			},
			"the archive has no libthread_db, and that of this host is of EM_X86_64, the core " +
				"of EM_AARCH64", "or inspect the core on a host of EM_AARCH64 and glibc 2.43", "",
		},
		{
			"a thread db that does not load, the host glibc", facts{
				Release: r390, GDB: gdb15, ThreadDB: true, NoLoad: noLoad, CoreGlibc: g243,
				HostGlibc: g243,
			}, "", "", "",
		},
		{
			"a thread db that cannot be told of", facts{
				Release: r390, GDB: gdb15, ThreadDB: true, ThreadDBUnknown: "cannot read x",
				CoreGlibc: g243, HostGlibc: g239,
			}, "", "", "whether gdb loads the libthread_db of the archive: cannot read x",
		},
		{
			"no thread db", facts{
				Release: r390, GDB: gdb15, ThreadDB: false, CoreGlibc: g243, HostGlibc: g239,
			},
			"the archive has no libthread_db, and that of this host is of glibc 2.39, the core " +
				"of 2.43", instead243, "",
		},
		{
			"no thread db, an older core", facts{
				Release: r390, GDB: gdb15, CoreGlibc: g239, HostGlibc: g244,
			},
			"the archive has no libthread_db, and that of this host is of glibc 2.44, the core " +
				"of 2.39", "or inspect the core on a host of glibc 2.39", "",
		},
		{
			"no thread db, the host glibc", facts{
				Release: r390, GDB: gdb15, CoreGlibc: g243, HostGlibc: g243,
			}, "", "", "",
		},
		{
			"no thread db, the host glibc and machine", facts{
				Release: r390, GDB: gdb15, CoreMachine: amd64, HostMachine: amd64,
				CoreGlibc: g243, HostGlibc: g243,
			}, "", "", "",
		},
		{
			"no core libc", facts{
				Release: r390, GDB: gdb15, CoreGlibcUnknown: "the archive has no libc.so.6",
				HostGlibc: g243,
			}, "", "", "the glibc of the core: the archive has no libc.so.6",
		},
		{
			"no core glibc", facts{
				Release: r390, GDB: gdb15, CoreGlibcUnknown: "no version of glibc in /a/libc.so.6",
				HostGlibc: g243,
			}, "", "", "the glibc of the core: no version of glibc in /a/libc.so.6",
		},
		{
			"no host glibc", facts{
				Release: r390, GDB: gdb15, ThreadDB: true, NoLoad: noLoad, CoreGlibc: g243,
				HostGlibcUnknown: "no libc.so.6 under /lib",
			}, "", "", "the glibc of this host: no libc.so.6 under /lib",
		},
	}

	for _, testCase := range cases {
		problem, instead, unknown := testCase.facts.tlsProblem()
		assert.Equal(t, testCase.problem, problem, testCase.name)
		assert.Equal(t, testCase.instead, instead, testCase.name)
		assert.Equal(t, testCase.unknown, unknown, testCase.name)
	}
}

// tlsWarnings are the warning and the hint of a core of Tarantool 3.9.0
// read by gdb 15.1 on a host of glibc 2.39 of an archive with no
// libthread_db, of glibc 2.43, with the commands of the extensions of
// tarantool-gdb-tools or without.
func tlsWarnings(gdbTools bool) []string {
	commands := "the lj-* commands given no lua_State will not find it"
	if gdbTools {
		commands = "lj-bt will show L=0x0 (none) and the Lua frames of the VM entries alone, and " +
			commands
	}

	return []string{
		"gdb 15.1 will not read tarantool_L of this core of Tarantool 3.9.0, the Lua state of " +
			"the main thread, thread-local since 3.7: " + commands,
		"hint: gdb before 17.1 needs libthread_db for it, and the archive has no libthread_db, " +
			"and that of this host is of glibc 2.39, the core of 2.43; use gdb 17.1 or later, or " +
			"inspect the core on a host of glibc 2.43",
	}
}

func TestWarnThreadLocalLua(t *testing.T) {
	t.Parallel()

	facts := threadDBFacts{
		Release: release{3, 9, 0}, GDB: debuggerVersion{15, 1}, ThreadDB: false,
		CoreGlibc: glibcVersion{2, 43}, HostGlibc: glibcVersion{2, 39},
	}

	for _, gdbTools := range []bool{false, true} {
		log, recorder := logtest.New(t)

		warnThreadLocalLua(log, facts, gdbTools)
		assert.Equal(t, tlsWarnings(gdbTools), warnings(recorder))
	}

	log, recorder := logtest.New(t)

	warnThreadLocalLua(log, threadDBFacts{
		Release: release{3, 9, 0}, GDB: debuggerVersion{15, 1}, ThreadDB: false,
		CoreGlibcUnknown: "the archive has no libc.so.6", HostGlibc: glibcVersion{2, 39},
		LuaStateUnlisted: "neither the .symtab nor the .dynsym of /a/tarantool lists tarantool_L",
	}, true)
	assert.Empty(t, warnings(recorder))
	assert.Contains(t, recorder.Messages(), "cannot tell whether gdb reads the Lua state of "+
		"the main thread of the core, tarantool_L: cannot tell the glibc of the core: the "+
		"archive has no libc.so.6")
	assert.Contains(t, recorder.Messages(), "the symbols of the tarantool of the core do not "+
		"tell whether tarantool_L is thread-local, the version of the core tells, thread-local "+
		"since 3.7: neither the .symtab nor the .dynsym of /a/tarantool lists tarantool_L")

	// A development version of gdb 17 may read it on its own.
	log, recorder = logtest.New(t)

	snapshot := facts

	snapshot.GDB = debuggerVersion{17, 0, 50}

	warnThreadLocalLua(log, snapshot, false)
	assert.Equal(t, []string{
		"gdb 17.0.50 may not read tarantool_L of this core of Tarantool 3.9.0, the Lua state of " +
			"the main thread, thread-local since 3.7: if it does not, the lj-* commands given no " +
			"lua_State will not find it",
		"hint: gdb before 17.1 needs libthread_db for it, unless it is a development version " +
			"of 17 that reads it on its own, and the archive has no libthread_db, and that of " +
			"this host is of glibc 2.39, the core of 2.43; use gdb 17.1 or later, or inspect " +
			"the core on a host of glibc 2.43",
	}, warnings(recorder))

	// The stacks of zeros, whose own warning comes first, and a core whose
	// version cannot be told, whose symbol tells.
	log, recorder = logtest.New(t)

	warnThreadLocalLua(log, threadDBFacts{
		LuaState: luaStateThreadLocal, GDB: debuggerVersion{16, 3}, ZeroedStacks: 1,
		ThreadDB: true,
	}, false)
	assert.Equal(t, []string{
		"gdb 16.3 will not read tarantool_L of this core, the Lua state of the main thread, " +
			"thread-local since 3.7: the lj-* commands given no lua_State will not find it",
		"hint: gdb before 17.1 needs libthread_db for it, and libthread_db fails on the stacks " +
			"of zeros of this core; use gdb 17.1 or later, take the core the kernel writes, or " +
			"write it with gcore-pages of tarantool-gdb-tools",
	}, warnings(recorder))
}

// symbolSections returns the sections of a symbol table of the type typ,
// SHT_SYMTAB or SHT_DYNSYM, and of its string table, which is to be the
// section strtab of the file, the first being 1, the table the one after
// it: a symbol of each of symbols, by its name and of the type of its value,
// defined in the section 1.
//
//nolint:gosec // The sizes and the indexes are those of test data.
func symbolSections(t *testing.T, typ elf.SectionType, strtab uint32,
	symbols map[string]elf.SymType,
) []testSection {
	t.Helper()

	names := []byte{0}

	var table bytes.Buffer

	require.NoError(t, binary.Write(&table, binary.LittleEndian, elf.Sym64{}))

	for name, symType := range symbols {
		require.NoError(t, binary.Write(&table, binary.LittleEndian, elf.Sym64{
			Name: uint32(len(names)), Info: elf.ST_INFO(elf.STB_GLOBAL, symType), Shndx: 1,
			Value: 0x1e0, Size: 8,
		}))

		names = append(names, name+"\x00"...)
	}

	prefix := ".sym"
	if typ == elf.SHT_DYNSYM {
		prefix = ".dyn"
	}

	return []testSection{
		{name: prefix + "str", typ: elf.SHT_STRTAB, data: names, align: 1},
		{
			name: prefix + "tab", typ: typ, data: table.Bytes(), align: 8, link: strtab, info: 1,
			entsize: elf.Sym64Size,
		},
	}
}

// writeLuaStateTarantool writes the tarantool of the artefacts in dir: code
// and its debugging information, a build ID with build, and a .symtab and a
// .dynsym of the symbols of each that is not nil.
//
//nolint:gosec // The indexes of the sections are those of test data.
func writeLuaStateTarantool(t *testing.T, dir string, build bool,
	symtab, dynsym map[string]elf.SymType,
) {
	t.Helper()

	sections := []testSection{text, debugInfo}

	if build {
		sections = append(sections, buildIDSection(t))
	}

	if symtab != nil {
		sections = append(sections, symbolSections(t, elf.SHT_SYMTAB,
			uint32(len(sections)+1), symtab)...)
	}

	if dynsym != nil {
		sections = append(sections, symbolSections(t, elf.SHT_DYNSYM,
			uint32(len(sections)+1), dynsym)...)
	}

	writeELF(t, filepath.Join(dir, "tarantool"), sections, nil)
}

// TestReadLuaState checks that tarantool_L is thread-local as the type of
// its symbol says, of the .symtab of the tarantool, else of its .dynsym,
// else of the .symtab of its debug file in the archive, and that symbols
// that list it nowhere say why.
func TestReadLuaState(t *testing.T) {
	t.Parallel()

	var (
		tls   = map[string]elf.SymType{"main": elf.STT_FUNC, luaStateName: elf.STT_TLS}
		data  = map[string]elf.SymType{luaStateName: elf.STT_OBJECT, "main": elf.STT_FUNC}
		other = map[string]elf.SymType{"main": elf.STT_FUNC}
	)

	cases := []struct {
		name           string
		symtab, dynsym map[string]elf.SymType
		state          luaState
	}{
		{"thread-local", tls, nil, luaStateThreadLocal},
		{"of the process", data, nil, luaStateGlobal},
		{"of the .symtab first", data, tls, luaStateGlobal},
		{"of the .dynsym", other, tls, luaStateThreadLocal},
		{"of the .dynsym alone", nil, data, luaStateGlobal},
	}

	for _, testCase := range cases {
		dir := t.TempDir()
		writeLuaStateTarantool(t, dir, false, testCase.symtab, testCase.dynsym)

		state, unlisted := readLuaState(dir)
		assert.Equal(t, testCase.state, state, testCase.name)
		assert.Empty(t, unlisted, testCase.name)
	}

	debugFile := debugFilePath(filepath.Join("usr", "lib", "debug"), testBuildID)

	// A stripped tarantool and its debug file.
	dir := t.TempDir()
	writeLuaStateTarantool(t, dir, true, nil, nil)
	writeELF(t, filepath.Join(dir, debugFile), append([]testSection{strippedText},
		symbolSections(t, elf.SHT_SYMTAB, 2, tls)...), nil)

	state, unlisted := readLuaState(dir)
	assert.Equal(t, luaStateThreadLocal, state)
	assert.Empty(t, unlisted)

	unlistedIn := "neither the .symtab nor the .dynsym of " + filepath.Join(dir, "tarantool") +
		" lists tarantool_L"

	writeELF(t, filepath.Join(dir, debugFile), append([]testSection{strippedText},
		symbolSections(t, elf.SHT_SYMTAB, 2, other)...), nil)

	state, unlisted = readLuaState(dir)
	assert.Equal(t, luaStateUnlisted, state)
	assert.Equal(t, unlistedIn+", and neither does the .symtab of "+
		filepath.Join(dir, debugFile), unlisted)

	require.NoError(t, os.Remove(filepath.Join(dir, debugFile)))

	state, unlisted = readLuaState(dir)
	assert.Equal(t, luaStateUnlisted, state)
	assert.Contains(t, unlisted, unlistedIn+", and its debug file cannot be read: ")

	dir = t.TempDir()
	writeLuaStateTarantool(t, dir, false, other, nil)

	state, unlisted = readLuaState(dir)
	assert.Equal(t, luaStateUnlisted, state)
	assert.Equal(t, "neither the .symtab nor the .dynsym of "+filepath.Join(dir, "tarantool")+
		" lists tarantool_L, and it has no build ID to find a debug file by", unlisted)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tarantool"), []byte("ELF"), 0o755))

	state, unlisted = readLuaState(dir)
	assert.Equal(t, luaStateUnlisted, state)
	assert.Contains(t, unlisted, "cannot read "+filepath.Join(dir, "tarantool")+": ")
}

// TestReadLuaStateOfTarantool checks the symbol tarantool_L of the
// tarantool of PATH against its version: thread-local since 3.7, as the
// symbols of the static builds of 3.6.4 and 3.9.0 tell, STT_OBJECT and
// STT_TLS. A tarantool whose symbols do not list it is skipped.
func TestReadLuaStateOfTarantool(t *testing.T) {
	t.Parallel()

	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("no tarantool in PATH")
	}

	out, err := exec.CommandContext(t.Context(), tarantool, "--version").Output()
	require.NoError(t, err)

	line, _, _ := strings.Cut(string(out), "\n")

	version, ok := parseVersion(line)
	require.True(t, ok, line)

	parsed, ok := parseRelease(version.Release)
	require.True(t, ok, line)

	dir := t.TempDir()
	require.NoError(t, os.Symlink(tarantool, filepath.Join(dir, "tarantool")))

	state, unlisted := readLuaState(dir)
	if state == luaStateUnlisted {
		t.Skipf("the symbols of %s tell nothing of tarantool_L: %s", tarantool, unlisted)
	}

	want := luaStateGlobal
	if parsed.compare(threadLocalLua) >= 0 {
		want = luaStateThreadLocal
	}

	assert.Equal(t, want, state, line)
	t.Logf("%s: tarantool_L is %v", line, map[luaState]string{
		luaStateThreadLocal: "thread-local", luaStateGlobal: "of the process",
	}[state])
}

// TestLibcVersion checks that the version of a libc is that of its banner,
// which glibc 2.37 and 2.40, defining no version of their own on x86_64,
// need, and else the highest of its version definitions of a release, and
// that a file with neither, or no ELF file, has none.
func TestLibcVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for name, testCase := range map[string]struct {
		lib  versionedLibrary
		want glibcVersion
	}{
		"2.40 of its banner": {libc(glibc239, "2.40"), glibcVersion{2, 40}},
		"2.37 of its banner": {libc(glibc239[:4], "2.37"), glibcVersion{2, 37}},
		"a snapshot": {
			versionedLibrary{
				defines: glibc243, banner: "GNU C Library (GNU libc) development release " +
					"version 2.43.9000.\n",
			}, glibcVersion{2, 43},
		},
		"no banner": {versionedLibrary{defines: glibc241}, glibcVersion{2, 41}},
	} {
		version, err := libcVersion(testCase.lib.write(t, filepath.Join(dir, name)))
		require.NoError(t, err, name)
		assert.Equal(t, testCase.want, version, name)
	}

	path := versionedLibrary{defines: []string{"GLIBC_PRIVATE"}}.write(t,
		filepath.Join(dir, "none"))

	_, err := libcVersion(path)
	require.ErrorIs(t, err, errNoGlibcVersion)
	require.ErrorContains(t, err, "no banner, and no version definition GLIBC_x.y")

	path = filepath.Join(dir, libcName)
	writeELF(t, path, []testSection{text}, nil)

	_, err = libcVersion(path)
	require.ErrorIs(t, err, errNoGlibcVersion)

	require.NoError(t, os.WriteFile(path, []byte("text"), 0o644))

	_, err = libcVersion(path)
	require.ErrorContains(t, err, "cannot read "+path)
}

// hostLibc returns the libc of this host and the directories of its
// libraries, as hostLibraries finds them, or skips the test on a host of
// another C library.
func hostLibc(t *testing.T) (string, elf.Machine, []string) {
	t.Helper()

	machine, dirs := hostLibraries()

	path := findLibrary(dirs, libcName, machine)
	if path == "" {
		t.Skipf("no %s of this host under %s", libcName, strings.Join(dirs, ", "))
	}

	return path, machine, dirs
}

// TestHostLibcVersion checks that the version of the libc of this host is
// the one getconf tells of its glibc, whatever it is.
func TestHostLibcVersion(t *testing.T) {
	t.Parallel()

	path, _, _ := hostLibc(t)

	out, err := exec.CommandContext(t.Context(), "getconf", "GNU_LIBC_VERSION").Output()
	if err != nil {
		t.Skipf("getconf tells no glibc: %v", err)
	}

	version, err := libcVersion(path)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(string(out)), "glibc "+version.String())
}

// TestThreadDBLoadProblem checks that a libthread_db loads where the libc
// defines every version it needs but the weak ones, whatever the versions
// of glibc, and does not where one is missing or it is of another machine,
// and that a library it needs the host has none of tells nothing.
func TestThreadDBLoadProblem(t *testing.T) {
	t.Parallel()

	host := t.TempDir()
	ubuntu := libc(glibc239, "2.39").write(t, filepath.Join(host, libcName))
	dir := t.TempDir()

	problem, err := threadDBLoadProblem(threadDB(threadDB243).write(t,
		filepath.Join(dir, "243")), elf.EM_X86_64, []string{host})
	require.NoError(t, err)
	assert.Equal(t, "it needs GLIBC_ABI_DT_X86_64_PLT, which "+ubuntu+" does not define", problem)

	// The libthread_db of Debian 13 needs versions of 2.36 and before.
	problem, err = threadDBLoadProblem(threadDB(threadDB241).write(t,
		filepath.Join(dir, "241")), elf.EM_X86_64, []string{host})
	require.NoError(t, err)
	assert.Empty(t, problem)

	weak := versionedLibrary{
		defines: nil, needs: map[string][]string{libcName: threadDB241},
		weak: map[string][]string{libcName: {"GLIBC_ABI_DT_X86_64_PLT"}}, banner: "",
	}

	problem, err = threadDBLoadProblem(weak.write(t, filepath.Join(dir, "weak")), elf.EM_X86_64,
		[]string{host})
	require.NoError(t, err)
	assert.Empty(t, problem, "a weak version is not needed")

	// The libc of 2.41 defines what that of 2.43 needs.
	debian := t.TempDir()
	libc(glibc241, "2.41").write(t, filepath.Join(debian, libcName))

	problem, err = threadDBLoadProblem(filepath.Join(dir, "243"), elf.EM_X86_64, []string{debian})
	require.NoError(t, err)
	assert.Empty(t, problem)

	arm := threadDB(threadDB241).write(t, filepath.Join(dir, "arm"))
	setMachine(t, arm, elf.EM_AARCH64)

	problem, err = threadDBLoadProblem(arm, elf.EM_X86_64, []string{host})
	require.NoError(t, err)
	assert.Equal(t, "it is a library of EM_AARCH64, this host is of EM_X86_64", problem)

	other := versionedLibrary{
		defines: nil, needs: map[string][]string{"libother.so.1": {"OTHER_1"}}, weak: nil,
		banner: "",
	}

	_, err = threadDBLoadProblem(other.write(t, filepath.Join(dir, "other")), elf.EM_X86_64,
		[]string{host})
	require.ErrorIs(t, err, errNoHostLibrary)
	require.ErrorContains(t, err, "it needs libother.so.1, and none is under "+host)

	// A libc of another machine is none of the host.
	_, err = threadDBLoadProblem(filepath.Join(dir, "243"), elf.EM_AARCH64, []string{host})
	require.NoError(t, err)

	setMachine(t, filepath.Join(dir, "241"), elf.EM_AARCH64)

	_, err = threadDBLoadProblem(filepath.Join(dir, "241"), elf.EM_AARCH64, []string{host})
	require.ErrorIs(t, err, errNoHostLibrary)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "text"), []byte("text"), 0o644))

	_, err = threadDBLoadProblem(filepath.Join(dir, "text"), elf.EM_X86_64, []string{host})
	require.ErrorContains(t, err, "cannot read ")
}

// TestHostThreadDBLoads checks that the libthread_db of this host loads with
// its libc, as gdb of this host loads it.
func TestHostThreadDBLoads(t *testing.T) {
	t.Parallel()

	_, machine, dirs := hostLibc(t)

	path := findLibrary(dirs, threadDBName, machine)
	if path == "" {
		t.Skipf("no %s of this host under %s", threadDBName, strings.Join(dirs, ", "))
	}

	problem, err := threadDBLoadProblem(path, machine, dirs)
	require.NoError(t, err)
	assert.Empty(t, problem)
}

// TestFindArchiveLibc checks that the libc of an archive is the one under
// its path, not one of the sources, of the extensions or of the debug
// files, and that an archive without one has none.
func TestFindArchiveLibc(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	assert.Empty(t, findArchiveLibc(dir))

	for _, name := range []string{
		"sources/build/libc.so.6", "ext/libc.so.6", "usr/lib/debug/libc.so.6",
		"usr/lib/x86_64-linux-gnu/libc.so.6",
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("libc"), 0o644))
	}

	assert.Equal(t, filepath.Join(dir, "usr/lib/x86_64-linux-gnu/libc.so.6"),
		findArchiveLibc(dir))
}

// TestInspectWarnsOfThreadLocalLua checks that inspect warns, before gdb
// starts, of a core of Tarantool 3.7 or later that gdb older than 17.1 will
// not read tarantool_L of, by the libthread_db and the libc of the archive
// and the libc of the host, and of none gdb 17.1 reads or of an older
// Tarantool. The libraries are test ones, of the versions of the libraries
// of Ubuntu 24.04, Debian 13 and Ubuntu 26.04.
//
//nolint:paralleltest // Replaces hostLibraries.
func TestInspectWarnsOfThreadLocalLua(t *testing.T) {
	saved := hostLibraries

	t.Cleanup(func() { hostLibraries = saved })

	type archive = threadDBArchive

	run := func(t *testing.T, version string, gdb debuggerVersion, artefacts archive,
		host versionedLibrary, gdbTools bool,
	) []string {
		t.Helper()

		hostDir := t.TempDir()
		host.write(t, filepath.Join(hostDir, libcName))

		hostLibraries = func() (elf.Machine, []string) {
			return elf.EM_X86_64, []string{hostDir}
		}

		dir := t.TempDir()
		writeArtefacts(t, dir)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o644))

		lib := filepath.Join(dir, "usr", "lib", "x86_64-linux-gnu")
		artefacts.libc.write(t, filepath.Join(lib, libcName))

		if artefacts.threadDB {
			threadDB(threadDB243).write(t, filepath.Join(lib, threadDBName))
		}

		if artefacts.machine != elf.EM_NONE {
			setMachine(t, filepath.Join(lib, libcName), artefacts.machine)

			if artefacts.threadDB {
				setMachine(t, filepath.Join(lib, threadDBName), artefacts.machine)
			}
		}

		if artefacts.core != "" {
			data, err := os.ReadFile(artefacts.core)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "coredump"), data, 0o600))
		}

		if artefacts.symtab != nil {
			writeLuaStateTarantool(t, dir, false, artefacts.symtab, nil)
		}

		backend := newRecordingDebugger(debuggerGDB)

		backend.probe.Version = gdb

		_, server := newFakeGitHub(t)
		res, recorder := testResolver(t, server, t.TempDir())

		var stdout bytes.Buffer

		err := inspectWith(t.Context(), output.Streams{
			In: strings.NewReader(""), Out: &stdout, Err: &stdout,
		}, res, backend, InspectOptions{Path: dir, SourceDir: "", GDBTools: gdbTools})
		require.NoError(t, err, stdout.String())

		return warnings(recorder)
	}

	var (
		gdb15, gdb16, gdb17 = debuggerVersion{15, 1}, debuggerVersion{16, 3}, debuggerVersion{17, 1}
		ubuntu24            = libc(glibc239, "2.39")
		debian13            = libc(glibc241, "2.41")
		ubuntu26            = libc(glibc243, "2.43")
		withThreadDB        = archive{libc: ubuntu26, threadDB: true}
		withoutThreadDB     = archive{libc: ubuntu26, threadDB: false}
	)

	// The libthread_db of the archive needs a version the libc of Ubuntu
	// 24.04 does not define, and that of the host is of another glibc:
	// gdb 15 and gdb 16 alike read no thread-local variable.
	for _, gdb := range []debuggerVersion{gdb15, gdb16} {
		got := run(t, v390, gdb, withThreadDB, ubuntu24, false)
		require.Len(t, got, 2, gdb)
		assert.Equal(t, "gdb "+gdb.String()+" will not read tarantool_L of this core of "+
			"Tarantool 3.9.0, the Lua state of the main thread, thread-local since 3.7: the "+
			"lj-* commands given no lua_State will not find it", got[0])
		assert.Contains(t, got[1], "hint: gdb before 17.1 needs libthread_db for it, and the "+
			"libthread_db of the archive does not load here, it needs GLIBC_ABI_DT_X86_64_PLT, "+
			"which ")
		assert.Contains(t, got[1], " does not define, and that of this host is of glibc 2.39, "+
			"the core of 2.43; use gdb 17.1 or later, or inspect the core on a host of glibc "+
			"2.43 or later")
	}

	// No libthread_db in the archive, and that of the host of another
	// glibc.
	got := run(t, v390, gdb15, withoutThreadDB, ubuntu24, true)
	require.Len(t, got, 2)
	assert.Contains(t, got[0], ": lj-bt will show L=0x0 (none) and the Lua frames of the VM "+
		"entries alone")
	assert.Equal(t, "hint: gdb before 17.1 needs libthread_db for it, and the archive has no "+
		"libthread_db, and that of this host is of glibc 2.39, the core of 2.43; use gdb 17.1 "+
		"or later, or inspect the core on a host of glibc 2.43", got[1])

	// The libthread_db of the archive loads with the libc of Debian 13,
	// older than its glibc; that of the host matches a core of its glibc;
	// gdb 17 and Tarantool 3.6 need neither.
	assert.Empty(t, run(t, v390, gdb15, withThreadDB, debian13, true))
	assert.Empty(t, run(t, v390, gdb16, withThreadDB, ubuntu26, true))
	assert.Empty(t, run(t, v390, gdb15, withoutThreadDB, ubuntu26, true))
	assert.Empty(t, run(t, v390, gdb17, withoutThreadDB, ubuntu24, true))
	assert.Empty(t, run(t, v364, gdb15, withoutThreadDB, ubuntu24, true))

	// glibc 2.40 defines no version of its own: the banners tell the core
	// and the host of the same glibc.
	glibc240 := archive{libc: libc(glibc239, "2.40"), threadDB: false}
	assert.Empty(t, run(t, v390, gdb15, glibc240, libc(glibc239, "2.40"), true))

	checkInspectOfZeroedStacks(t, run)
	checkInspectOfOtherMachine(t, run)
	checkInspectOfSnapshot(t, run)
	checkInspectOfLuaStateSymbol(t, run)
}

// The versions of the cores of TestInspectWarnsOfThreadLocalLua.
const (
	v390 = "Tarantool 3.9.0-0-g8b9a0f59a95b\n"
	v364 = "Tarantool 3.6.4-0-g1234567\n"
)

// threadDBArchive is what an archive of TestInspectWarnsOfThreadLocalLua
// has: a libc and its libthread_db, of x86_64 or of the machine machine,
// the core of artefacts or the one at core, and the tarantool of artefacts
// or one whose symbols are symtab.
type threadDBArchive struct {
	libc     versionedLibrary
	threadDB bool
	machine  elf.Machine
	core     string
	symtab   map[string]elf.SymType
}

// threadDBRun is the run of TestInspectWarnsOfThreadLocalLua: the warnings
// of inspect of a core of the version of artefacts, read by gdb of a host
// of the libc host.
type threadDBRun func(t *testing.T, version string, gdb debuggerVersion,
	artefacts threadDBArchive, host versionedLibrary, gdbTools bool) []string

// v390Warning is the warning of a core of Tarantool 3.9.0 whose tarantool_L
// gdb will not read.
func v390Warning(gdb string) string {
	return "gdb " + gdb + " will not read tarantool_L of this core of Tarantool 3.9.0, the Lua " +
		"state of the main thread, thread-local since 3.7: the lj-* commands given no " +
		"lua_State will not find it"
}

// zeroedHint is the hint of a core with stacks of zeros of which gdb will not
// read tarantool_L.
const zeroedHint = "hint: gdb before 17.1 needs libthread_db for it, and libthread_db fails on " +
	"the stacks of zeros of this core; use gdb 17.1 or later, take the core the kernel writes, " +
	"or write it with gcore-pages of tarantool-gdb-tools"

// checkInspectOfZeroedStacks checks that inspect warns of tarantool_L of a
// core with stacks of zeros, which no libthread_db reads, after the warning
// of the stacks: of gdb 16.3 of Debian 13, whose libc the libthread_db of
// the archive loads with, as on Ubuntu 25.10, of the glibc of the core,
// and of none gdb 17.1 reads or of an older Tarantool.
func checkInspectOfZeroedStacks(t *testing.T, run threadDBRun) {
	t.Helper()

	core := stacksCore(t, elf.EM_X86_64, true)
	gdb16, gdb17 := debuggerVersion{16, 3}, debuggerVersion{17, 1}

	zeroed := append(slices.Clone(stacksWarnings), v390Warning("16.3"), zeroedHint)

	debian13 := threadDBArchive{libc: libc(glibc243, "2.43"), threadDB: true, core: core}
	assert.Equal(t, zeroed, run(t, v390, gdb16, debian13, libc(glibc241, "2.41"), false))

	glibc242 := append(slices.Clone(glibc241), "GLIBC_2.42")
	ubuntu2510 := threadDBArchive{libc: libc(glibc242, "2.42"), threadDB: true, core: core}
	assert.Equal(t, zeroed, run(t, v390, gdb16, ubuntu2510, libc(glibc242, "2.42"), false))

	// The fake GitHub has the extensions of 3.9.0 alone: those of
	// tarantool-gdb-tools are for the other versions.
	assert.Equal(t, stacksWarnings, run(t, v390, gdb17, debian13, libc(glibc241, "2.41"), false))
	assert.Equal(t, stacksWarnings, run(t, v364, gdb16, debian13, libc(glibc241, "2.41"), true))
}

// checkInspectOfOtherMachine checks that the hint of a core whose
// libthread_db is of another machine than the host names the machine to
// inspect it on, whatever the glibc of the host.
func checkInspectOfOtherMachine(t *testing.T, run threadDBRun) {
	t.Helper()

	arm := threadDBArchive{libc: libc(glibc243, "2.43"), threadDB: true, machine: elf.EM_AARCH64}
	assert.Equal(t, []string{
		v390Warning("15.1"),
		"hint: gdb before 17.1 needs libthread_db for it, and the libthread_db of the archive " +
			"does not load here, it is a library of EM_AARCH64, this host is of EM_X86_64, and " +
			"that of this host is of EM_X86_64, the core of EM_AARCH64; use gdb 17.1 or later, " +
			"or inspect the core on a host of EM_AARCH64 and glibc 2.43 or later",
	}, run(t, v390, debuggerVersion{15, 1}, arm, libc(glibc243, "2.43"), false))
}

// checkInspectOfSnapshot checks that a snapshot of gdb 17, before 17.1,
// may not read tarantool_L, and is warned of as one that may.
func checkInspectOfSnapshot(t *testing.T, run threadDBRun) {
	t.Helper()

	snapshot, ok := parseDebuggerVersion("17.0.50.20251101-git")
	require.True(t, ok)

	got := run(t, v390, snapshot, threadDBArchive{libc: libc(glibc243, "2.43")},
		libc(glibc239, "2.39"), false)
	require.Len(t, got, 2)
	assert.Equal(t, "gdb 17.0.50 may not read tarantool_L of this core of Tarantool 3.9.0, the "+
		"Lua state of the main thread, thread-local since 3.7: if it does not, the lj-* commands "+
		"given no lua_State will not find it", got[0])
	assert.Equal(t, "hint: gdb before 17.1 needs libthread_db for it, unless it is a "+
		"development version of 17 that reads it on its own, and the archive has no "+
		"libthread_db, and that of this host is of glibc 2.39, the core of 2.43; use gdb 17.1 "+
		"or later, or inspect the core on a host of glibc 2.43", got[1])
}

// checkInspectOfLuaStateSymbol checks that the symbol tarantool_L of the
// tarantool of the core tells whether it is thread-local before the version
// does: a development build of 3.7 may have either. The extensions are those
// of tarantool-gdb-tools, which the fake GitHub has for any version.
func checkInspectOfLuaStateSymbol(t *testing.T, run threadDBRun) {
	t.Helper()

	const entrypoint = "Tarantool 3.7.0-entrypoint-12-g1234567\n"

	ubuntu24 := libc(glibc239, "2.39")
	global := threadDBArchive{
		libc: libc(glibc243, "2.43"), symtab: map[string]elf.SymType{luaStateName: elf.STT_OBJECT},
	}
	assert.Empty(t, run(t, entrypoint, debuggerVersion{15, 1}, global, ubuntu24, true))

	threadLocal := global

	threadLocal.symtab = map[string]elf.SymType{luaStateName: elf.STT_TLS}

	got := run(t, v364, debuggerVersion{15, 1}, threadLocal, ubuntu24, true)
	require.Len(t, got, 2)
	assert.Equal(t, "gdb 15.1 will not read tarantool_L of this core of Tarantool 3.6.4, the "+
		"Lua state of the main thread, thread-local since 3.7: lj-bt will show L=0x0 (none) and "+
		"the Lua frames of the VM entries alone, and the lj-* commands given no lua_State will "+
		"not find it", got[0])
}
