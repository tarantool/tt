package coredump

import (
	"debug/elf"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// otherBuildID is a build ID of another executable than testBuildID.
const otherBuildID = "0123456789abcdef0123456789abcdef01234567"

// openObject writes the test ELF file o into a temporary directory and
// opens it.
func openObject(t *testing.T, o testObject) *elf.File {
	t.Helper()

	file, err := elf.Open(o.write(t, filepath.Join(t.TempDir(), "elf")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })

	return file
}

func TestHasSignature(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		table   elf.SectionType
		symbols []testSymbol
		want    bool
	}{
		{
			name: "dynamic", table: elf.SHT_DYNSYM,
			symbols: []testSymbol{
				{name: "main", section: ".text"}, {name: tarantoolSignature, section: ".text"},
			},
			want: true,
		},
		{
			name: "symtab", table: elf.SHT_SYMTAB,
			symbols: []testSymbol{{name: tarantoolSignature, section: ".text"}}, want: true,
		},
		{
			name: "not in .text", table: elf.SHT_DYNSYM,
			symbols: []testSymbol{{name: tarantoolSignature, section: ".data"}}, want: false,
		},
		{
			name: "other", table: elf.SHT_DYNSYM,
			symbols: []testSymbol{{name: "tarantool", section: ".text"}}, want: false,
		},
		{name: "no symbols", table: elf.SHT_DYNSYM, symbols: nil, want: false},
	}

	for _, testCase := range cases {
		file := openObject(t, testObject{
			typ: elf.ET_EXEC, base: 0x400000, entry: 0, notes: nil, dynamic: 0,
			table: testCase.table, symbols: testCase.symbols,
		})
		assert.Equal(t, testCase.want, hasSignature(file), testCase.name)
	}
}

func TestOpenExecutable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, typ := range []elf.Type{elf.ET_EXEC, elf.ET_DYN} {
		path := testObject{
			typ: typ, base: 0, entry: 0, notes: nil, dynamic: 0, table: 0, symbols: nil,
		}.write(t, filepath.Join(dir, typ.String()))

		file, err := openExecutable(path)
		require.NoError(t, err, typ)
		require.NoError(t, file.Close())
	}

	relocatable := testObject{
		typ: elf.ET_REL, base: 0, entry: 0, notes: nil, dynamic: 0, table: 0, symbols: nil,
	}.write(t, filepath.Join(dir, "object.o"))

	_, err := openExecutable(relocatable)
	require.ErrorIs(t, err, errNotELF)
	require.ErrorContains(t, err, "an ELF file of type ET_REL, not an executable")

	text := filepath.Join(dir, "text")
	require.NoError(t, os.WriteFile(text, []byte("#!/bin/sh\n"), 0o755))

	for _, path := range []string{text, filepath.Join(dir, "missing")} {
		_, err = openExecutable(path)
		require.ErrorIs(t, err, errNotELF, path)
	}
}

func TestCoreMismatch(t *testing.T) {
	t.Parallel()

	const base = 0x7f0000000000

	exe := testExecutable(t, base, 0x123, testBuildID)
	core := testExecutableCore(t, exe, base, "/usr/bin/tarantool").open(t)

	assert.Empty(t, coreMismatch(core, openObject(t, exe)))
	assert.Equal(t, "the executable of the core has build ID "+testBuildID+", the binary "+
		otherBuildID, coreMismatch(core, openObject(t, testExecutable(t, base, 0x123,
		otherBuildID))))

	// Without a build ID on a side the layouts are compared: a binary with
	// its build ID removed is another file.
	layout := "the entry point or the program headers of the executable of the core differ " +
		"from those of the binary"
	assert.Equal(t, layout, coreMismatch(core, openObject(t, testExecutable(t, base, 0x123, ""))))

	noID := testExecutable(t, base, 0x123, "")

	core = testExecutableCore(t, noID, base, "/usr/bin/tarantool").open(t)

	assert.Empty(t, coreMismatch(core, openObject(t, noID)))
	assert.Equal(t, layout, coreMismatch(core, openObject(t, testExecutable(t, base, 0x456, ""))))

	// A core without the image of its executable tells nothing.
	noImage := testExecutableCore(t, exe, base, "/usr/bin/tarantool")

	noImage.segments = nil

	assert.Empty(t, coreMismatch(noImage.open(t), openObject(t, testExecutable(t, base, 0x123,
		otherBuildID))))
}

// testLoaderBase is where a test core of a program the dynamic loader ran
// has the loader.
const testLoaderBase = 0x7ffff7fc0000

// testLoaderCore returns a test core of exe, mapped from path, that the
// dynamic loader ran, ld.so PROGRAM, with a link map of objects: the
// kernel ran the loader, of the build ID otherBuildID and no interpreter,
// and loaded no interpreter, so AT_PHDR is that of the loader and AT_BASE
// is 0; the loader mapped exe.
func testLoaderCore(t *testing.T, exe testObject, path string, objects []linkMapEntry) testCore {
	t.Helper()

	loader := testExecutable(t, testLoaderBase, 0x456, otherBuildID)
	core := testLinkMapCore(t, exe, path, objects, false)

	core.segments = append(core.segments, testSegment{
		vaddr: testLoaderBase, data: loader.bytes(t)[:testPage],
	})
	core.mappings = append(core.mappings, fileMapping{
		start: testLoaderBase, end: testLoaderBase + testPage, offset: 0,
		path: "/usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
	})
	core.auxv = [][2]uint64{{atPhdr, testLoaderBase + 64}, {atBase, 0}}

	return core
}

func TestCoreMismatchOfLoader(t *testing.T) {
	t.Parallel()

	exe := testLinkMapExecutable(t)

	exe.interp = testLoaderName

	objects := []linkMapEntry{
		{name: "", dynamic: testDataBase},
		{name: "/lib/x86_64-linux-gnu/libc.so.6", addr: 0x7ffff7c00000, dynamic: 0x7ffff7e11940},
		{name: testLoaderName, addr: testLoaderBase, dynamic: 0x7ffff7ffce60},
	}
	core := testLoaderCore(t, exe, "/usr/bin/tarantool", objects).open(t)

	// The program is the tarantool the loader mapped: the build ID of the
	// loader was compared with that of the tarantool.
	binary := openObject(t, exe)

	assert.Empty(t, coreMismatch(core, binary))
	assert.Equal(t, objects[1:], loadedObjects(io.Discard, core, binary))

	// A core of the loader that ran another tarantool is refused.
	const thirdBuildID = "fedcba9876543210fedcba9876543210fedcba98"

	other := testLinkMapExecutable(t)

	other.interp = testLoaderName
	other.notes = buildIDNote(t, thirdBuildID)

	assert.Equal(t, "the executable of the core has build ID "+otherBuildID+", the binary "+
		thirdBuildID, coreMismatch(core, openObject(t, other)))

	// A static executable has no interpreter, and is the program itself.
	static := testExecutable(t, testExecBase, 0x123, testBuildID)
	staticCore := testExecutableCore(t, static, testExecBase, "/usr/bin/tarantool").open(t)

	assert.Empty(t, coreMismatch(staticCore, openObject(t, static)))
	assert.Equal(t, "the executable of the core has build ID "+testBuildID+", the binary "+
		thirdBuildID, coreMismatch(staticCore, openObject(t, other)))
}

func TestProbeArtefactsOfLoader(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// A position-independent tarantool, which glibc of the reports named
	// by its path in the link map of a program the loader ran.
	exe := testLinkMapExecutable(t)

	exe.typ = elf.ET_DYN
	exe.interp = testLoaderName

	tarantool := exe.write(t, filepath.Join(root, "bin", "tarantool"))
	libc := writeLibrary(t, filepath.Join(root, "lib", "libc.so.6"), "pthread_create")
	threadDB := writeLibrary(t, filepath.Join(root, "lib", threadDBName))

	objects := []linkMapEntry{
		{name: tarantool, dynamic: testDataBase},
		{name: libc, addr: 0x7ffff7c00000, dynamic: 0x7ffff7c00100},
	}
	core := testLoaderCore(t, exe, tarantool, objects)

	core.mappings = append(core.mappings, fileMapping{
		start: 0x7ffff7c00000, end: 0x7ffff7c01000, offset: 0, path: libc,
	})

	var notices strings.Builder

	artefacts, err := probeArtefacts(&notices, PackOptions{
		Core: core.write(t, filepath.Join(root, "core")), Executable: tarantool, Dir: "", PID: 0,
		Time: "",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{libc, threadDB}, artefacts.files)
	assert.Empty(t, notices.String())
}

func TestProbeArtefactsChecks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	exe := testExecutable(t, 0x400000, 0x123, testBuildID)
	tarantool := exe.write(t, filepath.Join(dir, "tarantool"))
	core := testExecutableCore(t, exe, 0x400000, tarantool).write(t, filepath.Join(dir, "core"))

	artefacts, err := probeArtefacts(io.Discard, PackOptions{
		Core: core, Executable: tarantool, Dir: "", PID: 0, Time: "",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, tarantool, artefacts.executable)

	other := testExecutable(t, 0x400000, 0x123, otherBuildID).write(t,
		filepath.Join(dir, "other"))
	notTarantool := testObject{
		typ: elf.ET_EXEC, base: 0x400000, entry: 0, notes: nil, dynamic: 0, table: elf.SHT_DYNSYM,
		symbols: []testSymbol{{name: "main", section: ".text"}},
	}.write(t, filepath.Join(dir, "true"))

	cases := []struct {
		name, core, executable string
		err                    error
		msg                    string
	}{
		{
			name: "no core", core: filepath.Join(dir, "missing"), executable: tarantool,
			err: errNotCore, msg: "not a core dump: " + filepath.Join(dir, "missing") + ": ",
		},
		{
			name: "not a core", core: tarantool, executable: tarantool, err: errNotCore,
			msg: "not a core dump: " + tarantool + ": the given COREDUMP file is not a valid " +
				"core dump (see core(5)) or not even an ELF file (see elf(5)): not an ELF core " +
				"file: an ELF file of type ET_EXEC",
		},
		{
			name: "not an ELF", core: core, executable: core, err: errNotELF,
			msg: "not an ELF file: " + core + ": the given BINARY file is an ELF file of type " +
				"ET_CORE, not an executable",
		},
		{
			name: "not a tarantool", core: core, executable: notTarantool, err: errNotTarantool,
			msg: "not a Tarantool binary: " + notTarantool + ": there is no signature symbol, " +
				"tarantool_version, in the binary file; check the BINARY file with `" +
				notTarantool + " --help`",
		},
		{
			name: "other tarantool", core: core, executable: other, err: errCoreMismatch,
			msg: "core dump file is produced by the different Tarantool executable: looks like '" +
				core + "' is not generated by `" + other + "': the executable of the core has " +
				"build ID " + testBuildID + ", the binary " + otherBuildID + "; check that the " +
				"given COREDUMP is produced by the specified BINARY",
		},
	}

	for _, testCase := range cases {
		_, err := probeArtefacts(io.Discard, PackOptions{
			Core: testCase.core, Executable: testCase.executable, Dir: "", PID: 0, Time: "",
		}, nil)
		require.ErrorIs(t, err, testCase.err, testCase.name)
		assert.Contains(t, err.Error(), testCase.msg, testCase.name)
	}
}

//nolint:paralleltest // Changes PATH.
func TestFindTarantool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	_, err := findTarantool("")
	require.ErrorIs(t, err, errNoTarantool)

	path, err := findTarantool("bin/tarantool")
	require.NoError(t, err)
	assert.Equal(t, "bin/tarantool", path)

	tarantool := filepath.Join(dir, "tarantool")
	require.NoError(t, os.WriteFile(tarantool, []byte("#!/bin/sh\n"), 0o755))

	path, err = findTarantool("")
	require.NoError(t, err)
	assert.Equal(t, tarantool, path)
}

// writeLibrary writes a test shared object to path that defines symbols in
// .text, and returns path.
func writeLibrary(t *testing.T, path string, symbols ...string) string {
	t.Helper()

	defined := make([]testSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		defined = append(defined, testSymbol{name: symbol, section: ".text"})
	}

	return testObject{
		typ: elf.ET_DYN, base: 0, entry: 0, notes: nil, dynamic: 0, table: elf.SHT_DYNSYM,
		symbols: defined,
	}.write(t, path)
}

func TestSharedLibraries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tarantool := testExecutable(t, 0, 0x123, testBuildID).write(t,
		filepath.Join(root, "bin", "tarantool"))
	libc := writeLibrary(t, filepath.Join(root, "lib", "libc.so.6"), "pthread_create")
	realLibm := writeLibrary(t, filepath.Join(root, "usr", "lib", "libm.so.6"))
	extra := writeLibrary(t, filepath.Join(root, "usr", "lib", "libextra.so"))
	gone := writeLibrary(t, filepath.Join(root, "usr", "lib", "libgone.so"))
	object := testObject{
		typ: elf.ET_REL, base: 0, entry: 0, notes: nil, dynamic: 0, table: 0, symbols: nil,
	}.write(t, filepath.Join(root, "usr", "lib", "object.o"))
	archive := filepath.Join(root, "usr", "lib", "locale-archive")
	hardlink := filepath.Join(root, "usr", "lib", "tarantool")

	// The loader opened libm by a symbolic link to it, as /lib of a merged
	// /usr is; the kernel records the real path.
	libm := filepath.Join(root, "lib", "libm.so.6")
	require.NoError(t, os.Symlink(realLibm, libm))
	require.NoError(t, os.WriteFile(archive, []byte("locales"), 0o644))
	require.NoError(t, os.Link(tarantool, hardlink))
	require.NoError(t, os.Remove(gone))

	// A library replaced on disk since it was loaded, as an upgrade of a
	// package replaces it.
	replaced := writeLibrary(t, filepath.Join(root, "usr", "lib", "libreplaced.so"))

	paths := []string{
		tarantool, libc, realLibm, extra, gone + " (deleted)", "/dev/zero (deleted)",
		"/memfd:jit (deleted)", archive, object, hardlink, "anon_inode:[eventfd]",
		filepath.Join(root, "new\nline.so"), replaced + " (deleted)",
	}
	mappings := make([]fileMapping, 0, len(paths))

	for index, path := range paths {
		start := 0x7f0000000000 + uint64(index)*testPage

		mappings = append(mappings, fileMapping{
			start: start, end: start + testPage, offset: 0, path: path,
		})
	}

	core := testCore{
		segments: nil, mappings: mappings, auxv: [][2]uint64{{atPhdr, 0x7f0000000040}},
		notes: nil,
	}.open(t)

	// The copy of a module Tarantool loaded and deleted, in no mapping of
	// the core.
	module := filepath.Join(root, "tmp", "tntQwErTy", "module.so")

	objects := []linkMapEntry{
		{name: "", dynamic: 0x7f0000000100},
		{name: "linux-vdso.so.1", dynamic: 0x7ffff7fc1000},
		{name: libm, dynamic: 0x7f0000002100},
		{name: libc, dynamic: 0x7f0000001100},
		{name: libm, dynamic: 0x7f0000002100},
		{name: gone, dynamic: 0x7f0000004100},
		{name: replaced, dynamic: 0x7f000000c100},
		{name: module, dynamic: 0x7f1000000100},
		{name: archive, dynamic: 0x7f0000007100},
	}

	var notices strings.Builder

	assert.Equal(t, []string{libm, libc, extra},
		sharedLibraries(&notices, core, objects, tarantool))

	gdbNote := ". gdb will have no symbols of it.\n"
	assert.Equal(t, "The shared library "+gone+" of the core is not packed: the file the "+
		"process loaded is not on the host: "+gone+" (deleted)"+gdbNote+
		"The shared library "+replaced+" of the core is not packed: the file the process "+
		"loaded is not on the host: "+replaced+" (deleted)"+gdbNote+
		"The shared library "+module+" of the core is not packed: the file the process "+
		"loaded is not on the host: stat "+module+": no such file or directory"+gdbNote+
		"The shared library "+archive+" of the core is not packed: the file the process "+
		"loaded is not on the host: not an ELF shared object"+gdbNote, notices.String())

	// Without a link map, the paths of the files mapped.
	notices.Reset()
	assert.Equal(t, []string{libc, realLibm, extra},
		sharedLibraries(&notices, core, nil, tarantool))
	assert.Empty(t, notices.String())
}

func TestSharedLibrariesBuildID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tarantool := testExecutable(t, 0, 0x123, testBuildID).write(t,
		filepath.Join(root, "bin", "tarantool"))

	// library returns a test shared object of the build ID id, none when it
	// is empty.
	library := func(id string) testObject {
		var notes []byte

		if id != "" {
			notes = buildIDNote(t, id)
		}

		return testObject{typ: elf.ET_DYN, notes: notes, table: elf.SHT_DYNSYM}
	}

	lib := func(name string) string { return filepath.Join(root, "lib", name) }

	// The library the process loaded, of testBuildID, and what is at its
	// path now: the same build ID in a new file, which the mapping says is
	// deleted; a link re-pointed to a new version; a file of another build
	// ID in place of the deleted one; and files with no build ID on a side.
	loaded := library(testBuildID)
	cases := []struct {
		name, mapped string
		disk, core   testObject
	}{
		{name: "libsame.so", mapped: lib("libsame.so") + deletedSuffix, disk: loaded, core: loaded},
		{name: "liblink.so", mapped: lib("libold.so"), disk: library(otherBuildID), core: loaded},
		{
			name: "libreplaced.so", mapped: lib("libreplaced.so") + deletedSuffix,
			disk: library(otherBuildID), core: loaded,
		},
		{
			name: "libnoid.so", mapped: lib("libnoid.so") + deletedSuffix, disk: library(""),
			core: library(""),
		},
		{
			name: "liboneid.so", mapped: lib("liboneid.so") + deletedSuffix, disk: loaded,
			core: library(""),
		},
		{name: "libkept.so", mapped: lib("libkept.so"), disk: library(""), core: loaded},
	}

	objects := make([]linkMapEntry, 0, len(cases))
	mappings := make([]fileMapping, 0, len(cases))
	segments := make([]testSegment, 0, len(cases))

	for index, testCase := range cases {
		start := 0x7f0000000000 + uint64(index)*0x10000

		testCase.disk.write(t, lib(testCase.name))

		objects = append(objects, linkMapEntry{
			name: lib(testCase.name), addr: start, dynamic: start + 0x100,
		})
		mappings = append(mappings, fileMapping{
			start: start, end: start + testPage, offset: 0, path: testCase.mapped,
		})
		segments = append(segments, testSegment{
			vaddr: start, data: testCase.core.bytes(t)[:testPage],
		})
	}

	// The link of a new version, and the old version it pointed to, which
	// the process loaded and which is still there.
	loaded.write(t, lib("libold.so"))
	require.NoError(t, os.Rename(lib("liblink.so"), lib("libnew.so")))
	require.NoError(t, os.Symlink(lib("libnew.so"), lib("liblink.so")))

	core := testCore{segments: segments, mappings: mappings, auxv: nil, notes: nil}.open(t)

	var notices strings.Builder

	assert.Equal(t, []string{lib("libsame.so"), lib("libkept.so"), lib("libold.so")},
		sharedLibraries(&notices, core, objects, tarantool))

	changed := " of the core is not packed: the file at its path is not the one the process " +
		"loaded: it has build ID " + otherBuildID + ", the process loaded " + testBuildID +
		". gdb will have no symbols of it.\n"
	gone := " of the core is not packed: the file the process loaded is not on the host: "
	assert.Equal(t, "The shared library "+lib("liblink.so")+changed+
		"The shared library "+lib("libreplaced.so")+changed+
		"The shared library "+lib("libnoid.so")+gone+lib("libnoid.so")+deletedSuffix+
		". gdb will have no symbols of it.\n"+
		"The shared library "+lib("liboneid.so")+gone+lib("liboneid.so")+deletedSuffix+
		". gdb will have no symbols of it.\n", notices.String())
}

func TestSharedLibrariesUnreadable(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a file of any mode")
	}

	root := t.TempDir()
	tarantool := testExecutable(t, 0, 0x123, testBuildID).write(t,
		filepath.Join(root, "bin", "tarantool"))
	lib := writeLibrary(t, filepath.Join(root, "lib", "libsecret.so"))
	require.NoError(t, os.Chmod(lib, 0))

	core := testCore{
		segments: nil,
		mappings: []fileMapping{
			{start: 0x7f0000000000, end: 0x7f0000001000, offset: 0, path: lib},
		},
		auxv: nil, notes: nil,
	}.open(t)

	// The library is on the host, and is not read: the notice says why,
	// where it said the file was no ELF shared object.
	var notices strings.Builder

	assert.Empty(t, sharedLibraries(&notices, core, []linkMapEntry{
		{name: lib, dynamic: 0x7f0000000100},
	}, tarantool))
	assert.Equal(t, "The shared library "+lib+" of the core is not packed: cannot read it: "+
		"open "+lib+": permission denied. gdb will have no symbols of it.\n", notices.String())
}

func TestFindThreadDB(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stub := writeLibrary(t, filepath.Join(root, "stub", "libpthread.so.0"))
	libc := writeLibrary(t, filepath.Join(root, "libc", "libc.so.6"), "pthread_create")
	bare := writeLibrary(t, filepath.Join(root, "bare", "libc.so.6"), "pthread_create")
	other := writeLibrary(t, filepath.Join(root, "other", "libpthread.so.0"), "pthread_create")
	libm := writeLibrary(t, filepath.Join(root, "libm", "libm.so.6"), "pthread_create")
	system := filepath.Join(root, "system")
	empty := filepath.Join(root, "empty")

	for _, dir := range []string{
		filepath.Dir(stub), filepath.Dir(libc), filepath.Dir(other), filepath.Dir(libm), system,
	} {
		writeLibrary(t, filepath.Join(dir, threadDBName))
	}

	require.NoError(t, os.Mkdir(empty, 0o755))

	// The empty libpthread of glibc 2.34 is passed over for libc.
	assert.Equal(t, filepath.Join(root, "libc", threadDBName),
		findThreadDB([]string{libm, stub, libc}, []string{system}))

	// Next to the first thread library alone, then in the system
	// directories.
	assert.Equal(t, filepath.Join(system, threadDBName),
		findThreadDB([]string{bare, other}, []string{empty, system}))
	assert.Empty(t, findThreadDB([]string{bare, other}, []string{empty}))
	assert.Empty(t, findThreadDB(nil, nil))

	assert.Equal(t, []string{
		"/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/lib64", "/usr/lib64", "/lib",
		"/usr/lib",
	}, systemLibraryDirs(elf.EM_AARCH64))
	assert.Equal(t, []string{"/lib64", "/usr/lib64", "/lib", "/usr/lib"},
		systemLibraryDirs(elf.EM_MIPS))

	// The machines Debian and Ubuntu release for.
	for machine, tuple := range map[elf.Machine]string{
		elf.EM_X86_64: "x86_64-linux-gnu", elf.EM_AARCH64: "aarch64-linux-gnu",
		elf.EM_386: "i386-linux-gnu", elf.EM_ARM: "arm-linux-gnueabihf",
		elf.EM_PPC64: "powerpc64le-linux-gnu", elf.EM_S390: "s390x-linux-gnu",
		elf.EM_RISCV: "riscv64-linux-gnu",
	} {
		assert.Equal(t, []string{"/lib/" + tuple, "/usr/lib/" + tuple},
			systemLibraryDirs(machine)[:2], machine)
	}
}

func TestFindDebugFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	other := t.TempDir()
	path := filepath.Join(dir, ".build-id", testBuildID[:2], testBuildID[2:]+".debug")

	assert.Empty(t, findDebugFile([]string{dir}, testBuildID))

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("debug"), 0o644))

	assert.Equal(t, path, findDebugFile([]string{dir}, testBuildID))
	assert.Empty(t, findDebugFile([]string{dir}, ""))
	assert.Empty(t, findDebugFile([]string{dir}, "78"))
	assert.Empty(t, findDebugFile(nil, testBuildID))

	// The first directory that has it.
	assert.Equal(t, path, findDebugFile([]string{other, dir}, testBuildID))

	otherPath := filepath.Join(other, ".build-id", testBuildID[:2], testBuildID[2:]+".debug")

	require.NoError(t, os.MkdirAll(filepath.Dir(otherPath), 0o755))
	require.NoError(t, os.WriteFile(otherPath, []byte("debug"), 0o644))
	assert.Equal(t, otherPath, findDebugFile([]string{other, dir}, testBuildID))
}

// compileTarantool compiles a tarantool with cc into dir, an executable
// that defines tarantool_version with a GNU build ID, as a linker writes
// it, and returns its path.
func compileTarantool(t *testing.T, dir string) string {
	t.Helper()

	_, err := exec.LookPath("cc")
	if runtime.GOOS != "linux" || err != nil {
		t.Skip("needs cc on Linux to build an ELF executable")
	}

	source := filepath.Join(dir, "tarantool.c")
	tarantool := filepath.Join(dir, "tarantool")

	require.NoError(t, os.WriteFile(source, []byte("void tarantool_version(void) {}\n"+
		"int main(void) { tarantool_version(); return 0; }\n"), 0o644))

	out, err := exec.CommandContext(t.Context(), "cc", "-Wl,--build-id=sha1", "-o", tarantool,
		source).CombinedOutput()
	require.NoError(t, err, string(out))

	return tarantool
}

// noteBuildIDOf returns the build ID of the ELF file at path in hex, read
// from its .note.gnu.build-id by the layout of a GNU note: the sizes of
// the name and of the descriptor, the type, "GNU\0", and the descriptor.
func noteBuildIDOf(t *testing.T, path string) string {
	t.Helper()

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	section := file.Section(".note.gnu.build-id")
	require.NotNil(t, section, "no build ID in %s", path)

	data, err := section.Data()
	require.NoError(t, err)
	require.Greater(t, len(data), 16)

	size := file.ByteOrder.Uint32(data[4:8])

	return hex.EncodeToString(data[16 : 16+size])
}

// binaryCore returns a test core of the executable at path mapped at base:
// its first page, as Linux dumps it, and the mapping of the page.
func binaryCore(t *testing.T, path string, base uint64) testCore {
	t.Helper()

	image, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Greater(t, len(image), testPage)

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	// The program headers follow the ELF header of a linker.
	const programHeaders = 64

	return testCore{
		segments: []testSegment{{vaddr: base, data: image[:testPage]}},
		mappings: []fileMapping{{start: base, end: base + testPage, offset: 0, path: path}},
		auxv:     [][2]uint64{{atPhdr, base + programHeaders}},
		notes:    nil,
	}
}

// TestProbeArtefactsDebugFile checks that pack packs the debug file of the
// tarantool, found by its build ID in a debug directory of the probe, as
// usr/lib/debug/.build-id/<xx>/<rest>.debug of the archive, where gdb.sh
// points gdb to: the first -f, NAME=PATH. The tarantool is one cc built,
// with the build ID of its linker, and a test one; the debug directories
// are temporary ones, a host one being /usr/lib/debug, which a test cannot
// write to.
func TestProbeArtefactsDebugFile(t *testing.T) {
	t.Parallel()

	build := t.TempDir()
	exe := testExecutable(t, testExecBase, 0x123, testBuildID)

	// The tarantools by name: the path, the core and the build ID of each.
	tarantools := map[string]func(t *testing.T) (string, testCore, string){
		"test": func(t *testing.T) (string, testCore, string) {
			t.Helper()

			tarantool := exe.write(t, filepath.Join(build, "test", "tarantool"))

			return tarantool, testExecutableCore(t, exe, testExecBase, tarantool), testBuildID
		},
		"cc": func(t *testing.T) (string, testCore, string) {
			t.Helper()

			tarantool := compileTarantool(t, build)

			return tarantool, binaryCore(t, tarantool, 0x555555554000),
				noteBuildIDOf(t, tarantool)
		},
	}

	for name, tarantool := range tarantools {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path, core, id := tarantool(t)
			root := t.TempDir()
			empty := filepath.Join(root, "empty")
			debugDir := filepath.Join(root, "debug")
			debugFile := filepath.Join(debugDir, ".build-id", id[:2], id[2:]+".debug")
			member := "usr/lib/debug/.build-id/" + id[:2] + "/" + id[2:] + ".debug"

			require.NoError(t, os.MkdirAll(empty, 0o755))
			require.NoError(t, os.MkdirAll(filepath.Dir(debugFile), 0o755))
			require.NoError(t, os.WriteFile(debugFile, []byte("the debug file"), 0o644))

			opts := PackOptions{
				Core: core.write(t, filepath.Join(root, "core")), Executable: path, Dir: "", PID: 0,
				Time: "",
			}

			// The first debug directory that has the file.
			artefacts, err := probeArtefacts(io.Discard, opts, []string{empty, debugDir})
			require.NoError(t, err)
			require.NotEmpty(t, artefacts.files)
			assert.Equal(t, member+"="+debugFile, artefacts.files[0])
			assert.Equal(t, []string{"-f", member + "=" + debugFile}, artefacts.scriptArgs()[:2])

			// None without it.
			artefacts, err = probeArtefacts(io.Discard, opts, []string{empty})
			require.NoError(t, err)
			assert.NotContains(t, strings.Join(artefacts.files, "\n"), ".debug")
		})
	}
}

func TestProbeArtefactsFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	exe := testLinkMapExecutable(t)
	tarantool := exe.write(t, filepath.Join(root, "bin", "tarantool"))
	libm := writeLibrary(t, filepath.Join(root, "lib", "libm.so.6"))
	libc := writeLibrary(t, filepath.Join(root, "lib", "libc.so.6"), "pthread_create")
	threadDB := writeLibrary(t, filepath.Join(root, "lib", threadDBName))

	objects := []linkMapEntry{
		{name: "", dynamic: testDataBase},
		{name: libm, dynamic: 0x7f0000000100},
		{name: libc, dynamic: 0x7f0000001100},
	}

	core := testLinkMapCore(t, exe, tarantool, objects, false)

	for index, lib := range []string{libm, libc} {
		start := 0x7f0000000000 + uint64(index)*testPage

		core.mappings = append(core.mappings, fileMapping{
			start: start, end: start + testPage, offset: 0, path: lib,
		})
	}

	opts := PackOptions{
		Core: core.write(t, filepath.Join(root, "core")), Executable: tarantool, Dir: "", PID: 0,
		Time: "",
	}

	var notices strings.Builder

	artefacts, err := probeArtefacts(&notices, opts, nil)
	require.NoError(t, err)
	assert.Equal(t, packArtefacts{executable: tarantool, files: []string{libm, libc, threadDB}},
		artefacts)
	assert.Equal(t, []string{"-f", libm, "-f", libc, "-f", threadDB}, artefacts.scriptArgs())
	assert.Empty(t, notices.String())

	// A core whose link map cannot be read: the libraries come by the paths
	// of the files mapped, with a notice.
	core.segments = core.segments[:1]
	opts.Core = core.write(t, filepath.Join(root, "partial"))

	artefacts, err = probeArtefacts(&notices, opts, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{libm, libc, threadDB}, artefacts.files)
	assert.Contains(t, notices.String(), "Cannot read the link map of the dynamic loader in the "+
		"core: cannot read the dynamic section of the executable: ")

	// The first object of the link map of a position-independent tarantool
	// named by its path, as musl names it by AT_EXECFN, is the executable,
	// which the pack script packs: it is not packed again as a library.
	pie := testLinkMapExecutable(t)

	pie.typ = elf.ET_DYN

	pieTarantool := pie.write(t, filepath.Join(root, "pie", "tarantool"))
	named := slices.Clone(objects)

	named[0].name = pieTarantool

	pieCore := testLinkMapCore(t, pie, pieTarantool, named, false)

	pieCore.mappings = append(pieCore.mappings, core.mappings[2:]...)
	opts.Core = pieCore.write(t, filepath.Join(root, "pie", "core"))
	opts.Executable = pieTarantool

	artefacts, err = probeArtefacts(io.Discard, opts, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{libm, libc, threadDB}, artefacts.files)

	opts.Executable = tarantool

	// A core whose NT_FILE note is malformed is packed as one that maps no
	// file, by its link map, with a notice; it was refused as no core dump.
	malformed := testLinkMapCore(t, exe, tarantool, objects, false)

	malformed.mappings = nil
	malformed.notes = note("CORE", ntFile, []byte{1, 2, 3})
	opts.Core = malformed.write(t, filepath.Join(root, "malformed"))

	notices.Reset()

	artefacts, err = probeArtefacts(&notices, opts, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{libm, libc, threadDB}, artefacts.files)
	assert.Equal(t, "Cannot read the files the core maps from its NT_FILE note: malformed note: "+
		"NT_FILE of 3 bytes. The shared libraries are packed by the link map of the core "+
		"alone.\n", notices.String())
}

// gdbLibraries returns the shared libraries gdb lists for the core of
// executable, info shared, by their absolute paths, and the libthread_db
// it says it loads for the core, if any.
func gdbLibraries(t *testing.T, executable, core string) ([]string, string) {
	t.Helper()

	out, err := gdbCommand(t, "-batch", "-n", executable, "-c", core, "-ex", "info shared").
		Output()
	require.NoError(t, err)

	var libs []string

	listed := false

	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)

		switch {
		case strings.Contains(line, "Shared Object Library"):
			listed = true
		case listed && len(fields) > 0 && strings.HasPrefix(fields[len(fields)-1], "/"):
			libs = append(libs, fields[len(fields)-1])
		default:
		}
	}

	out, err = gdbCommand(t, "-n", executable, "-c", core, "-ex", "quit").Output()
	require.NoError(t, err)

	match := regexp.MustCompile(`Using host libthread_db library "([^"]+)"`).FindSubmatch(out)
	if match == nil {
		return libs, ""
	}

	return libs, string(match[1])
}

// gdbCommand returns the command of gdb with args, without LD_LIBRARY_PATH,
// which may point gdb to another libpython than its own.
func gdbCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "gdb", args...)

	cmd.Env = slices.DeleteFunc(os.Environ(), func(variable string) bool {
		return strings.HasPrefix(variable, "LD_LIBRARY_PATH=")
	})

	return cmd
}

// TestProbeOfGcore checks the shared libraries and the libthread_db pack
// finds in a core made by gcore of gdb against those gdb finds: the core
// of /bin/sh, killed by SIGSEGV under gdb.
func TestProbeOfGcore(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("gdb")
	if runtime.GOOS != "linux" || err != nil {
		t.Skip("needs gdb on Linux")
	}

	shell, err := filepath.EvalSymlinks("/bin/sh")
	require.NoError(t, err)

	core := filepath.Join(t.TempDir(), "core")

	out, _ := gdbCommand(t, "-batch", "-n", "-ex", "run", "-ex", "gcore "+core, "-ex", "kill",
		"--args", shell, "-c", "kill -SEGV $$").CombinedOutput()

	_, err = os.Stat(core)
	if err != nil {
		t.Skipf("gdb made no core of %s: %s", shell, out)
	}

	gdbLibs, gdbThreadDB := gdbLibraries(t, shell, core)
	require.NotEmpty(t, gdbLibs)

	dump, err := openCore(core)
	require.NoError(t, err)

	defer func() { _ = dump.Close() }()

	binary, err := elf.Open(shell)
	require.NoError(t, err)

	defer func() { _ = binary.Close() }()

	var notices strings.Builder

	libs := sharedLibraries(&notices, dump, loadedObjects(&notices, dump, binary), shell)
	assert.Empty(t, notices.String())
	requireLibrariesOfGdb(t, gdbLibs, libs)

	if gdbThreadDB != "" {
		assert.Equal(t, gdbThreadDB, findThreadDB(libs, systemLibraryDirs(dump.file.Machine)))
	}
}

// testLoaderName is the interpreter of the test executables of the link
// map tests, the name glibc gives the dynamic loader.
const testLoaderName = "/lib64/ld-linux-x86-64.so.2"

func TestLoadedObjectsFromBinary(t *testing.T) {
	t.Parallel()

	exe := testLinkMapExecutable(t)

	exe.interp = testLoaderName

	objects := []linkMapEntry{
		{name: "", dynamic: testDataBase},
		{name: "/lib/x86_64-linux-gnu/libc.so.6", dynamic: 0x7ffff7e00000},
		{name: "", dynamic: 0x7ffff7ffd000},
	}

	// glibc names the dynamic loader by the interpreter of the executable,
	// in its first page, which a coredump_filter without the ELF headers bit
	// leaves out of the core: the name is read from the binary, as gdb
	// reads it. The link map was dropped, and the libraries were packed by
	// the real paths of NT_FILE.
	core := testLinkMapCore(t, exe, "/usr/bin/tarantool", objects, false)

	setLinkMapName(&core, 2, testExecBase+exe.interpOffset())

	core.segments = core.segments[1:]

	var notices strings.Builder

	assert.Equal(t, []linkMapEntry{objects[1], {name: testLoaderName, dynamic: 0x7ffff7ffd000}},
		loadedObjects(&notices, core.open(t), openObject(t, exe)))
	assert.Empty(t, notices.String())
}

func TestLoadedObjectsUnreadName(t *testing.T) {
	t.Parallel()

	const (
		loaderBase = 0x7ffff7fc0000
		libcPath   = "/usr/lib/x86_64-linux-gnu/libc.so.6"
		unread     = 0x7fff00000000
	)

	exe := testLinkMapExecutable(t)

	exe.interp = testLoaderName

	objects := []linkMapEntry{
		{name: "", dynamic: testDataBase},
		{name: "/lib/x86_64-linux-gnu/libm.so.6", addr: 0x7ffff7e00000, dynamic: 0x7ffff7f98b98},
		{name: "", addr: 0x7ffff7c00000, dynamic: 0x7ffff7e11940},
		{name: "", addr: 0x7ffff7a00000, dynamic: 0x7ffff7a11940},
		{name: "", addr: loaderBase, dynamic: 0x7ffff7ffce60},
	}

	// The names of libc, of a library in no mapping and of the loader are
	// neither in the core nor in the binary: the rest of the link map is
	// kept, libc is named by the path of the file mapped at its dynamic
	// section, the loader, loaded at AT_BASE, by the interpreter of the
	// binary, and the other one left out. The whole map was dropped.
	core := testLinkMapCore(t, exe, "/usr/bin/tarantool", objects, false)

	for index := 2; index < len(objects); index++ {
		setLinkMapName(&core, index, unread+uint64(index))
	}

	core.auxv = append(core.auxv, [2]uint64{atBase, loaderBase})
	core.mappings = append(core.mappings,
		fileMapping{start: 0x7ffff7e11000, end: 0x7ffff7e12000, offset: 0x211000, path: libcPath},
		fileMapping{
			start: 0x7ffff7ffc000, end: 0x7ffff7ffd000, offset: 0x3b000,
			path: "/usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
		})

	var notices strings.Builder

	got := loadedObjects(&notices, core.open(t), openObject(t, exe))

	names := make([]string, 0, len(got))
	for _, object := range got {
		names = append(names, object.name)
	}

	assert.Equal(t, []string{objects[1].name, libcPath, testLoaderName}, names)

	notInCore := func(addr uint64) string {
		return fmt.Sprintf("the core dump holds no content at the address: %#x", addr)
	}

	assert.Equal(t, "Cannot read the name of the shared library loaded at 0x7ffff7c00000 in "+
		"the core: "+notInCore(unread+2)+". It is packed as "+libcPath+", which gdb may not "+
		"look it up by.\n"+
		"Cannot read the name of the shared library loaded at 0x7ffff7a00000 in the core: "+
		notInCore(unread+3)+". It is not packed: the core maps no file there.\n"+
		"Cannot read the name of the shared library loaded at 0x7ffff7fc0000 in the core: "+
		notInCore(unread+4)+". It is packed as "+testLoaderName+", which gdb may not look "+
		"it up by.\n", notices.String())
}

func TestLoadedObjectsOfStatic(t *testing.T) {
	t.Parallel()

	// A core of a static executable, with no dynamic section and no
	// PT_PHDR, as gcc -static links one, whose first page is left out has
	// no link map to read, and that is no notice: it said the link map
	// could not be read without AT_PHDR or PT_PHDR.
	static := testExecutable(t, testExecBase, 0x123, testBuildID)
	core := testExecutableCore(t, static, testExecBase, "/usr/bin/tarantool")

	core.segments = nil
	core.auxv = nil

	var notices strings.Builder

	assert.Empty(t, loadedObjects(&notices, core.open(t), openObject(t, static)))
	assert.Empty(t, notices.String())

	// A dynamic one gets the notice.
	dynamic := testLinkMapExecutable(t)

	core = testExecutableCore(t, dynamic, testExecBase, "/usr/bin/tarantool")

	core.segments = nil
	core.auxv = nil

	assert.Empty(t, loadedObjects(&notices, core.open(t), openObject(t, dynamic)))
	assert.Contains(t, notices.String(), "Cannot read the link map of the dynamic loader in the "+
		"core: ")
}

// filteredGcore returns a gcore of /bin/sh, killed by SIGSEGV under gdb,
// with coredump_filter 0x23: the bit of the ELF headers off, as the
// filters of anonymous memory alone have it. gcore of gdb honours it, as
// Linux does, and leaves the first page of a file mapped out of the core,
// unless the process wrote to the mapping of the page: a mapping with a
// page of its own, a copy of that of the file, is anonymous memory to both,
// dumped whole. It returns the path of the shell too.
//
// gdb runs the shell with displaced stepping off. On, gdb steps over each
// of its breakpoints in the dynamic loader, where it learns of the
// libraries loaded, on a copy of the instruction at the entry point of the
// program, which gives the mapping of the entry such a page. Linked for
// aarch64, the entry and the ELF headers are in the same mapping, and the
// first page of the shell, of Ubuntu 24.04 on arm64, was in the core; on
// x86-64 binutils links them into mappings of their own, -z separate-code.
// Off, gdb steps over a breakpoint where it is and writes nothing to the
// program.
func filteredGcore(t *testing.T) (string, string) {
	t.Helper()

	_, err := exec.LookPath("gdb")
	if runtime.GOOS != "linux" || err != nil {
		t.Skip("needs gdb on Linux")
	}

	shell, err := filepath.EvalSymlinks("/bin/sh")
	require.NoError(t, err)

	core := filepath.Join(t.TempDir(), "core")

	out, _ := gdbCommand(t, "-batch", "-n", "-ex", "set displaced-stepping off", "-ex", "run",
		"-ex", "gcore "+core, "-ex", "kill",
		"--args", shell, "-c", "echo 0x23 > /proc/$$/coredump_filter; kill -SEGV $$").
		CombinedOutput()

	_, err = os.Stat(core)
	if err != nil {
		t.Skipf("gdb made no core of %s: %s", shell, out)
	}

	return core, shell
}

// TestProbeOfFilteredGcore checks the names of the shared libraries pack
// finds in a gcore without the first page of the executable against those
// gdb gives: the name of the dynamic loader, in the first page of the
// executable, is read from the executable.
func TestProbeOfFilteredGcore(t *testing.T) {
	t.Parallel()

	core, shell := filteredGcore(t)

	dump, err := openCore(core)
	require.NoError(t, err)

	defer func() { _ = dump.Close() }()

	_, err = dump.executableImage()
	require.ErrorIs(t, err, errNotInCore, "the filter leaves the ELF headers out")

	binary, err := elf.Open(shell)
	require.NoError(t, err)

	defer func() { _ = binary.Close() }()

	gdbLibs, _ := gdbLibraries(t, shell, core)
	require.NotEmpty(t, gdbLibs)

	var notices strings.Builder

	libs := sharedLibraries(&notices, dump, loadedObjects(&notices, dump, binary), shell)
	assert.Empty(t, notices.String())
	requireLibrariesOfGdb(t, gdbLibs, libs)
}

// requireLibrariesOfGdb checks libs, the shared libraries pack finds for a
// core, against gdbLibs, those gdb lists for it: libs has every one of them,
// and first, in their order, as the link map comes first. A libs shorter
// than gdbLibs fails the check before it is sliced, even when it has every
// library of gdbLibs, which may list one twice.
func requireLibrariesOfGdb(test require.TestingT, gdbLibs, libs []string) {
	if helper, ok := test.(interface{ Helper() }); ok {
		helper.Helper()
	}

	require.Subset(test, libs, gdbLibs)
	require.GreaterOrEqual(test, len(libs), len(gdbLibs), "pack finds fewer libraries than "+
		"gdb lists: %v, gdb %v", libs, gdbLibs)
	require.Equal(test, gdbLibs, libs[:len(gdbLibs)], "the link map comes first, in its order")
}

// stoppingT is a require.TestingT that records the failures of a check and
// stops the goroutine of the check at a fatal one, as testing.T does.
type stoppingT struct {
	errors []string
}

// Errorf records a failure.
func (s *stoppingT) Errorf(format string, args ...any) {
	s.errors = append(s.errors, fmt.Sprintf(format, args...))
}

// FailNow stops the goroutine; the failure is recorded by Errorf.
func (*stoppingT) FailNow() {
	runtime.Goexit()
}

// runCheck runs check in a goroutine of its own with a stoppingT, and
// returns the stoppingT and what the check panicked with, if anything.
func runCheck(check func(t require.TestingT)) (*stoppingT, any) {
	var (
		recorder = &stoppingT{errors: nil}
		panicked any
	)

	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() { panicked = recover() }()

		check(recorder)
	}()

	<-done

	return recorder, panicked
}

// TestRequireLibrariesOfGdb checks the check of TestProbeOfGcore: a list of
// pack shorter than that of gdb fails it, where the check sliced the list
// out of range and panicked. gdb may list a library twice, which pack
// lists once.
func TestRequireLibrariesOfGdb(t *testing.T) {
	t.Parallel()

	const (
		loader = "/lib64/ld-linux-x86-64.so.2"
		libc   = "/lib/x86_64-linux-gnu/libc.so.6"
		libm   = "/lib/x86_64-linux-gnu/libm.so.6"
	)

	cases := []struct {
		name          string
		gdbLibs, libs []string
		ok            bool
	}{
		{name: "same", gdbLibs: []string{loader, libc}, libs: []string{loader, libc}, ok: true},
		{
			name: "more", gdbLibs: []string{loader, libc}, libs: []string{loader, libc, libm},
			ok: true,
		},
		{name: "missing", gdbLibs: []string{loader, libc, libm}, libs: []string{loader}, ok: false},
		{
			name: "twice in gdb", gdbLibs: []string{loader, libc, loader},
			libs: []string{loader, libc}, ok: false,
		},
		{name: "order", gdbLibs: []string{loader, libc}, libs: []string{libc, loader}, ok: false},
	}

	for _, testCase := range cases {
		recorder, panicked := runCheck(func(t require.TestingT) {
			requireLibrariesOfGdb(t, testCase.gdbLibs, testCase.libs)
		})
		require.Nil(t, panicked, testCase.name)
		assert.Equal(t, testCase.ok, recorder.errors == nil, "%s: %v", testCase.name,
			recorder.errors)
	}
}
