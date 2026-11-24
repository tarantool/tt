package coredump

import (
	"debug/elf"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
)

// The paths of the files the test core of mappedArtefacts maps, as the
// NT_FILE note of a core of Debian records them.
const (
	mappedExecutable = "/x/bin/tarantool"
	mappedLoader     = "/usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2"
	mappedLibc       = "/usr/lib/x86_64-linux-gnu/libc.so.6"
	mappedNoID       = "/usr/lib/libnoid.so"
	mappedChanged    = "/usr/lib/libchanged.so"
	mappedGone       = "/usr/lib/libgone.so" + deletedSuffix
	mappedLocale     = "/usr/lib/locale/locale-archive"
	mappedGconv      = "/usr/lib/x86_64-linux-gnu/gconv/gconv-modules.cache"
)

// The build IDs of the loader and of the library of mappedArtefacts the
// process loaded another build of.
const (
	loaderBuildID  = "1111111111111111111111111111111111111111"
	changedBuildID = "2222222222222222222222222222222222222222"
)

// testLibrary returns a test shared object of the build ID build, none when
// it is empty.
func testLibrary(t *testing.T, build string) testObject {
	t.Helper()

	var notes []byte

	if build != "" {
		notes = buildIDNote(t, build)
	}

	return testObject{typ: elf.ET_DYN, notes: notes, table: elf.SHT_DYNSYM}
}

// mappedArtefacts writes into dir the artefacts of a core of Debian: the
// tarantool, which the core maps as mappedExecutable, and the shared
// libraries under the names of the link map, the dynamic loader as
// /lib64/ld-linux-x86-64.so.2, which the core maps as mappedLoader, libc
// under the path the core maps, mappedLibc, a library the core holds no first
// page of under /lib/libnoid.so, and the library of another build ID than the
// process loaded under /lib/libchanged.so; and the core. The core maps the
// code of those libraries executable, and the code of a library deleted
// since, which the archive does not hold, and the locale archive and the
// gconv cache of glibc, which it holds neither, read-only.
func mappedArtefacts(t *testing.T, dir string) {
	t.Helper()

	exe := testLinkMapExecutable(t)
	exe.write(t, filepath.Join(dir, "tarantool"))

	type library struct {
		mapped, name string
		core, disk   testObject
		head         bool
		base         uint64
	}

	libraries := []library{
		{
			mapped: mappedLoader, name: "/lib64/ld-linux-x86-64.so.2",
			core: testLibrary(t, loaderBuildID), disk: testLibrary(t, loaderBuildID), head: true,
		},
		{
			mapped: mappedLibc, name: mappedLibc, core: testLibrary(t, otherBuildID),
			disk: testLibrary(t, otherBuildID), head: true,
		},
		{
			mapped: mappedNoID, name: "/lib/libnoid.so", core: testLibrary(t, ""),
			disk: testLibrary(t, otherBuildID), head: false,
		},
		{
			mapped: mappedChanged, name: "/lib/libchanged.so", core: testLibrary(t, changedBuildID),
			disk: testLibrary(t, otherBuildID), head: true,
		},
	}

	objects := make([]linkMapEntry, 0, 1+len(libraries))

	objects = append(objects, linkMapEntry{name: "", dynamic: testDataBase})

	for index := range libraries {
		lib := &libraries[index]

		lib.base = 0x7f0000000000 + uint64(index)*0x100000
		lib.disk.write(t, filepath.Join(dir, lib.name))

		objects = append(objects, linkMapEntry{
			name: lib.name, addr: lib.base, dynamic: lib.base + testPage + 0x100,
		})
	}

	core := testLinkMapCore(t, exe, mappedExecutable, objects, false)

	// The code of the tarantool, which the core holds none of.
	core.segments = append(core.segments, testSegment{
		vaddr: testExecBase + testPage, flags: elf.PF_R | elf.PF_X, size: 3 * testPage,
	})

	for _, lib := range libraries {
		core.mappings = append(core.mappings,
			fileMapping{start: lib.base, end: lib.base + testPage, offset: 0, path: lib.mapped},
			fileMapping{
				start: lib.base + testPage, end: lib.base + 4*testPage, offset: testPage,
				path: lib.mapped,
			})
		core.segments = append(core.segments, testSegment{
			vaddr: lib.base + testPage, flags: elf.PF_R | elf.PF_X, size: 3 * testPage,
		})

		if lib.head {
			core.segments = append(core.segments, testSegment{
				vaddr: lib.base, data: lib.core.bytes(t)[:testPage], flags: elf.PF_R,
			})
		}
	}

	// The files of no object.
	const others = 0x7f1000000000

	const (
		locale = others + 0x100000
		gconv  = others + 0x300000
	)

	core.mappings = append(core.mappings,
		fileMapping{start: others, end: others + testPage, offset: 0, path: mappedGone},
		fileMapping{start: locale, end: locale + 0x100000, offset: 0, path: mappedLocale},
		fileMapping{start: gconv, end: gconv + testPage, offset: 0, path: mappedGconv},
	)
	core.segments = append(core.segments,
		testSegment{vaddr: others, flags: elf.PF_R | elf.PF_X, size: testPage},
		testSegment{vaddr: locale, flags: elf.PF_R, size: 0x100000},
		testSegment{vaddr: gconv, flags: elf.PF_R, size: testPage},
	)

	core.write(t, filepath.Join(dir, "coredump"))
}

// TestLinkMappedFiles checks that the paths the core of an archive maps the
// tarantool and its shared libraries at are linked to them, where the
// archive has nothing: the executable, which tarantool is, by its build ID,
// the loader of a link map name other than the path of its file, by its build
// ID, and a library the core holds no first page of by its link map name.
// gdb warned that it could not open them. A library of another build ID is
// not linked, and the files not in the archive are told by whether the core
// maps code of them. A second run finds the links.
func TestLinkMappedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mappedArtefacts(t, dir)

	files, err := linkMappedFiles(dir)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		mappedExecutable: "tarantool",
		mappedLoader:     "lib64/ld-linux-x86-64.so.2",
		mappedNoID:       "lib/libnoid.so",
	}, files.linked)
	assert.Empty(t, files.failed)
	assert.Equal(t, []string{mappedChanged, mappedGone}, files.code)
	assert.Equal(t, []string{mappedLocale, mappedGconv}, files.data)

	for path, target := range files.linked {
		link := filepath.Join(dir, path)

		relative, err := os.Readlink(link)
		require.NoError(t, err, path)
		assert.False(t, filepath.IsAbs(relative), "%s -> %s", path, relative)

		linked, err := os.Stat(link)
		require.NoError(t, err, path)

		file, err := os.Stat(filepath.Join(dir, target))
		require.NoError(t, err, path)
		assert.True(t, os.SameFile(linked, file), path)
	}

	for _, path := range append(files.code, files.data...) {
		_, err := os.Lstat(filepath.Join(dir, path))
		require.ErrorIs(t, err, os.ErrNotExist, path)
	}

	again, err := linkMappedFiles(dir)
	require.NoError(t, err)
	assert.Empty(t, again.linked)
	assert.Equal(t, files.code, again.code)
	assert.Equal(t, files.data, again.data)
}

// TestLinkMappedFilesOtherBuild checks that the path of the executable is
// not linked to a tarantool of another build ID than the core has of it:
// gdb 16 and later refuse it, an older one reads it.
func TestLinkMappedFilesOtherBuild(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mappedArtefacts(t, dir)

	other := testLinkMapExecutable(t)

	other.notes = append(note("GNU", 1, []byte{0, 0, 0, 0, 3, 0, 0, 0}),
		buildIDNote(t, otherBuildID)...)
	other.write(t, filepath.Join(dir, "tarantool"))

	files, err := linkMappedFiles(dir)
	require.NoError(t, err)
	assert.NotContains(t, files.linked, mappedExecutable)
	assert.Contains(t, files.code, mappedExecutable)
}

// TestLinkMappedFilesReadOnly checks that the links of a directory that
// cannot be written to are among the failed, with what they were to link
// to.
func TestLinkMappedFilesReadOnly(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root writes into a directory of any mode")
	}

	dir := t.TempDir()
	mappedArtefacts(t, dir)

	for _, sub := range []string{"usr/lib/x86_64-linux-gnu", "usr/lib", "usr", "."} {
		require.NoError(t, os.Chmod(filepath.Join(dir, sub), 0o555))
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, sub), 0o755) })
	}

	files, err := linkMappedFiles(dir)
	require.NoError(t, err)
	assert.Empty(t, files.linked)

	failed := map[string]string{}

	for _, link := range files.failed {
		require.ErrorIs(t, link.err, os.ErrPermission, link.path)

		failed[link.path] = link.target
	}

	assert.Equal(t, map[string]string{
		mappedExecutable: "tarantool",
		mappedLoader:     "lib64/ld-linux-x86-64.so.2",
		mappedNoID:       "lib/libnoid.so",
	}, failed)
}

// TestLinkMappedFilesNoCore checks that artefacts without a core are an
// error, and that a path outside the sysroot is none of it.
func TestLinkMappedFilesNoCore(t *testing.T) {
	t.Parallel()

	_, err := linkMappedFiles(t.TempDir())
	require.Error(t, err)

	for _, path := range []string{"relative", "/", "/a/../b", "/a//b", "/a/./b"} {
		_, ok := sysrootName(path)
		assert.False(t, ok, path)
	}

	name, ok := sysrootName(mappedGone)
	assert.True(t, ok)
	assert.Equal(t, strings.TrimPrefix(mappedGone, "/"), name)
}

// TestLogMappedFiles checks what inspect says of the files the core maps
// before gdb starts: a line of the files gdb will warn of that hold no code,
// naming two, and of those it cannot link, which gdb will warn of too; the
// links and the files of code are debug lines. gdb 9.2 reads no NT_FILE and
// warns of none of them: of it, every line is a debug one that names no
// warning, and so with gdb 10.0.50, a snapshot that may be before the
// processing; gdb 10.1 and one of a version unknown warn.
func TestLogMappedFiles(t *testing.T) {
	t.Parallel()

	logged := func(files mappedFiles, gdb debuggerVersion) ([]string, []string) {
		log, recorder := logtest.New(t)

		logMappedFiles(log, files, gdb)

		var infos, debugs []string

		for _, record := range recorder.Records() {
			switch record.Level {
			case slog.LevelInfo:
				infos = append(infos, record.Message)
			case slog.LevelDebug:
				debugs = append(debugs, record.Message)
			default:
				t.Errorf("unexpected record: %v", record)
			}
		}

		return infos, debugs
	}

	infos := func(files mappedFiles) []string {
		got, _ := logged(files, debuggerVersion{17, 1, 0})

		return got
	}

	// files returns what linkMappedFiles returns of a core of a link, a
	// library of code not in the archive, the failed and the data.
	files := func(failed []failedLink, data ...string) mappedFiles {
		return mappedFiles{
			linked: map[string]string{mappedExecutable: "tarantool"}, failed: failed, data: data,
			code: []string{mappedGone},
		}
	}

	assert.Empty(t, infos(files(nil)))
	assert.Equal(t, []string{"gdb will warn of a file the core maps that is not in the " +
		"archive, /usr/lib/locale/locale-archive: it holds no code gdb needs"},
		infos(files(nil, mappedLocale)))
	assert.Equal(t, []string{"gdb will warn of 2 files the core maps that are not in the " +
		"archive, /usr/lib/locale/locale-archive and " +
		"/usr/lib/x86_64-linux-gnu/gconv/gconv-modules.cache: they hold no code gdb needs"},
		infos(files(nil, mappedLocale, mappedGconv)))
	assert.Equal(t, []string{"gdb will warn of 3 files the core maps that are not in the " +
		"archive, /usr/lib/locale/locale-archive, /usr/lib/locale/C.utf8/LC_CTYPE and 1 " +
		"more: they hold no code gdb needs"},
		infos(files(nil, mappedLocale, "/usr/lib/locale/C.utf8/LC_CTYPE", mappedGconv)))

	denied := errors.New("permission denied")
	failed := []failedLink{
		{path: mappedExecutable, target: "tarantool", err: denied},
		{path: mappedLoader, target: "lib64/ld-linux-x86-64.so.2", err: denied},
	}

	assert.Equal(t, []string{"cannot link /x/bin/tarantool, a file the core maps, to " +
		"tarantool of the archive: permission denied: gdb will warn that it can't open it"},
		infos(files(failed[:1])))
	assert.Equal(t, []string{"cannot link 2 files the core maps to those of the archive, " +
		"/x/bin/tarantool to tarantool: permission denied: gdb will warn that it can't open " +
		"them"}, infos(files(failed)))

	both := files(failed, mappedLocale, mappedGconv)

	for _, gdb := range []debuggerVersion{{10, 1, 0}, {}} {
		got, _ := logged(both, gdb)
		assert.Len(t, got, 2, gdb)
	}

	for _, gdb := range []debuggerVersion{{9, 2, 0}, {10, 0, 50}} {
		got, debugs := logged(both, gdb)
		assert.Empty(t, got, gdb)
		assert.Equal(t, []string{
			"the core maps /x/bin/tarantool, linked to tarantool of the artefacts",
			"the core maps 1 files of code that are not in the archive, which gdb has no " +
				"symbols of: " + mappedGone,
			"cannot link /x/bin/tarantool, a file the core maps, to tarantool of the archive: " +
				"permission denied",
			"cannot link " + mappedLoader + ", a file the core maps, to " +
				"lib64/ld-linux-x86-64.so.2 of the archive: permission denied",
			"the core maps 2 files that are not in the archive and hold no code gdb needs: " +
				mappedLocale + " and " + mappedGconv,
		}, debugs, gdb)

		for _, line := range debugs {
			assert.NotContains(t, line, "warn", gdb)
		}
	}
}
