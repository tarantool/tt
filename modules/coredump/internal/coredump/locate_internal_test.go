package coredump

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
)

// crashAt is when the crashes of the tests were, as their watchdog saw them,
// in the local time the pack script names an archive by.
//
//nolint:gosmopolitan // As above.
var crashAt = time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)

// testCrash is a crash of the PID 4242 by SIGSEGV with a core.
var testCrash = sdk.Crash{At: crashAt, PID: 4242, Signal: syscall.SIGSEGV, CoreDumped: true}

// testLocator returns a Locator of the pattern, with core_uses_pid as
// usesPID, on the host "host", whose reports of apport go into a directory
// of the test.
func testLocator(t *testing.T, pattern string, usesPID bool) *Locator {
	t.Helper()

	return &Locator{
		pattern: corePattern{pattern: pattern, usesPID: usesPID}, patternErr: nil,
		hostname: "host", crashDir: t.TempDir(), apportDir: t.TempDir(), bootID: testBootID,
		coredumpctl: runCoredumpctl,
	}
}

// testBootID is the ID of the boot of testLocator.
const testBootID = "0b5a1e2c-8f3d-4c6a-9e7b-1d2f3a4b5c6d"

// writeCoreOf writes a core of the process pid at path, written at written:
// an ELF core with its NT_PRPSINFO.
func writeCoreOf(t *testing.T, path string, pid uint32, written time.Time) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	testCore{segments: nil, mappings: nil, auxv: nil, notes: prpsinfoNote(pid)}.write(t, path)
	require.NoError(t, os.Chtimes(path, written, written))
}

// writeDumped writes a core of size bytes at path, written at written.
func writeDumped(t *testing.T, path string, size int, written time.Time) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o600))
	require.NoError(t, os.Chtimes(path, written, written))
}

// TestExpandPattern pins the expansion of a pattern of a file as core(5)
// tells it: what a crash tells is put in, the rest matches anything, and
// core_uses_pid appends .PID to a pattern without %p.
func TestExpandPattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern string
		usesPID bool
		glob    string
	}{
		{pattern: "core", usesPID: false, glob: "core"},
		{pattern: "core", usesPID: true, glob: "core.4242"},
		{pattern: "core.%p", usesPID: true, glob: "core.4242"},
		// %P, the PID of the initial namespace, is no %p for core_uses_pid.
		{pattern: "core.%P", usesPID: true, glob: "core.4242.4242"},
		{pattern: "/var/core/%e-%p-%t", usesPID: false, glob: "/var/core/*-4242-*"},
		{pattern: "/var/core/%E.%u.%g.%c.%d.%i.%I.%f", usesPID: false,
			glob: "/var/core/*.*.*.*.*.*.*.*"},
		{pattern: "core-%h-%s", usesPID: false, glob: "core-host-11"},
		{pattern: "100%%-%p", usesPID: false, glob: "100%-4242"},
		// An unknown specifier and a trailing % are dropped.
		{pattern: "core%z.%", usesPID: false, glob: "core."},
		{pattern: "[core]*?", usesPID: false, glob: `\[core]\*\?`},
	}

	for _, test := range cases {
		got := expandPattern(corePattern{pattern: test.pattern, usesPID: test.usesPID},
			testCrash, "host")
		assert.Equal(t, test.glob, got, test.pattern)
	}

	assert.Equal(t, `core-h\*st`, expandPattern(corePattern{pattern: "core-%h", usesPID: false},
		testCrash, "h*st"))
}

// TestReadCorePattern reads the pattern and core_uses_pid from a procfs of
// the test.
func TestReadCorePattern(t *testing.T) {
	t.Parallel()

	proc := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(proc, "sys", "kernel"), 0o755))

	_, err := readCorePattern(proc)
	require.ErrorContains(t, err, "cannot read kernel.core_pattern")

	require.NoError(t, os.WriteFile(filepath.Join(proc, corePatternFile),
		[]byte("|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h\n"), 0o644))

	pattern, err := readCorePattern(proc)
	require.NoError(t, err)
	assert.Equal(t, corePattern{
		pattern: "|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h", usesPID: false,
	}, pattern)

	require.NoError(t, os.WriteFile(filepath.Join(proc, coreUsesPIDFile), []byte("1\n"), 0o644))

	pattern, err = readCorePattern(proc)
	require.NoError(t, err)
	assert.True(t, pattern.usesPID)

	loc := newLocator(proc)
	got, err := loc.Pattern()
	require.NoError(t, err)
	assert.Equal(t, pattern.pattern, got)
}

// TestLocateFile finds the core of a pattern of a file: in the working
// directory of the process for a relative one, by its PID, written last
// before the crash; a file of another time and none are told apart.
func TestLocateFile(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: work}
	loc := testLocator(t, "core", true)

	_, err := loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "no file matches "+filepath.Join(work, "core.4242")+
		": it was removed or moved")

	path := filepath.Join(work, "core.4242")
	writeDumped(t, path, 4096, crashAt.Add(-time.Second))

	core, err := loc.Locate(t.Context(), inst, testCrash)
	require.NoError(t, err)
	assert.Equal(t, Core{
		Source: SourceFile, Path: path, Size: 4096, Archive: "", Time: crashAt.Add(-time.Second),
	}, core)

	// The core of a later crash of the same PID is not this one.
	writeDumped(t, path, 4096, crashAt.Add(time.Hour))

	_, err = loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, path+" is of another crash, written at "+
		crashAt.Add(time.Hour).Format(time.DateTime))

	// An absolute pattern, with what the crash does not tell matching
	// anything, takes the file written last of those before the crash.
	dir := t.TempDir()

	loc = testLocator(t, filepath.Join(dir, "core.%e.%p.%t"), true)

	writeDumped(t, filepath.Join(dir, "core.tarantool.4242.100"), 1, crashAt.Add(-time.Minute))
	writeDumped(t, filepath.Join(dir, "core.tarantool.4242.200"), 2, crashAt.Add(-time.Second))
	writeDumped(t, filepath.Join(dir, "core.tarantool.4243.300"), 3, crashAt.Add(-time.Second))

	core, err = loc.Locate(t.Context(), inst, testCrash)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "core.tarantool.4242.200"), core.Path)
	assert.Equal(t, int64(2), core.Size)
}

// TestLocateFileOfAnotherProcess checks a core file against the crash by the
// PID of its NT_PRPSINFO: the instances of an application share a working
// directory, and with a pattern with no %p, and core_uses_pid 0, the core of
// another instance that crashed then is where the one of the crash is to
// be. It is no core of the crash, and the one of the crash is taken of the
// files of the time a pattern of a time matches.
func TestLocateFileOfAnotherProcess(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: work}
	path := filepath.Join(work, "core")

	writeCoreOf(t, path, 4243, crashAt.Add(-time.Second))

	_, err := testLocator(t, "core", false).Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrOtherProcess)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.EqualError(t, err, "the core is not found: it is the core of another process: "+
		path+", written at "+crashAt.Add(-time.Second).Format(time.DateTime)+", is the core "+
		"of the PID 4243, not of the PID 4242 of the crash")

	writeCoreOf(t, path, 4242, crashAt.Add(-time.Second))

	core, err := testLocator(t, "core", false).Locate(t.Context(), inst, testCrash)
	require.NoError(t, err)
	assert.Equal(t, path, core.Path)

	// The core of the crash is the older of the two of the time.
	dir := t.TempDir()
	ours := filepath.Join(dir, "core.tarantool.100")

	writeCoreOf(t, ours, 4242, crashAt.Add(-2*time.Second))
	writeCoreOf(t, filepath.Join(dir, "core.tarantool.200"), 4243, crashAt.Add(-time.Second))

	core, err = testLocator(t, filepath.Join(dir, "core.%e.%t"), false).Locate(t.Context(), inst,
		testCrash)
	require.NoError(t, err)
	assert.Equal(t, ours, core.Path)
}

// TestLocateNoCore pins a crash with no core dumped, and a pattern that
// cannot be read or is of a pipe tt does not know.
func TestLocateNoCore(t *testing.T) {
	t.Parallel()

	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir()}
	crash := testCrash

	crash.CoreDumped = false

	_, err := testLocator(t, "core", true).Locate(t.Context(), inst, crash)
	require.ErrorIs(t, err, ErrNoCoreDumped)
	require.ErrorContains(t, err, "ulimit -c")

	broken := testLocator(t, "", false)

	broken.patternErr = errors.New("cannot read kernel.core_pattern: no procfs")

	_, err = broken.Locate(t.Context(), inst, testCrash)
	require.EqualError(t, err, "cannot read kernel.core_pattern: no procfs")

	_, err = testLocator(t, "|/usr/libexec/abrt-hook-ccpp %s %c %p %u %g %t e %P %I %h", false).
		Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, errUnknownPipe)
	require.ErrorContains(t, err, "/usr/libexec/abrt-hook-ccpp: look for the core where it "+
		"keeps them, by the PID 4242")
}

// fakeCoredumpctl puts a coredumpctl into a directory of PATH that prints
// list for --json=short list PID, after it writes its arguments into a
// file of the directory, and fails as it fails without a core when list is
// empty. It returns the file of the arguments.
//
//nolint:paralleltest // Sets PATH.
func fakeCoredumpctl(t *testing.T, list string) string {
	t.Helper()

	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" >> " + args + "\n"

	if list == "" {
		script += "echo 'No coredumps found.' >&2\nexit 1\n"
	} else {
		script += "cat <<'EOF'\n" + list + "\nEOF\n"
	}

	require.NoError(t, os.WriteFile(filepath.Join(dir, coredumpctlName), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return args
}

// coredumpctlTime is crashAt as coredumpctl prints a time, in microseconds.
func coredumpctlTime(at time.Time) string {
	return strconv.FormatInt(at.UnixMicro(), 10)
}

// TestLocateCoredumpctl finds the core systemd-coredump keeps by what
// coredumpctl, a fake of PATH, lists of the PID: the entry of the time of
// the crash, and why there is none.
//
//nolint:paralleltest // Sets PATH.
func TestLocateCoredumpctl(t *testing.T) {
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir()}
	pattern := "|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h"

	// A later process of the PID crashed five minutes after, within the
	// window: the entry of the crash is the one nearest its time.
	args := fakeCoredumpctl(t, `[{"time": `+coredumpctlTime(crashAt.Add(-time.Hour))+
		`, "pid": 4242, "uid": 1000, "gid": 1000, "sig": 6, "corefile": "present",
		"exe": "/usr/bin/tarantool", "size": 10},
		{"time": `+coredumpctlTime(crashAt.Add(-time.Second))+`, "pid": 4242, "uid": 1000,
		"gid": 1000, "sig": 11, "corefile": "present", "exe": "/usr/bin/tarantool",
		"size": 123456},
		{"time": `+coredumpctlTime(crashAt.Add(5*time.Minute))+`, "pid": 4242, "uid": 1000,
		"gid": 1000, "sig": 6, "corefile": "present", "exe": "/usr/bin/tarantool",
		"size": 99}]`)

	core, err := testLocator(t, pattern, false).Locate(t.Context(), inst, testCrash)
	require.NoError(t, err)
	assert.Equal(t, Core{
		Source: SourceCoredumpctl, Path: "", Size: 123456, Archive: "",
		Time: time.UnixMicro(crashAt.Add(-time.Second).UnixMicro()),
	}, core)

	called, err := os.ReadFile(args)
	require.NoError(t, err)
	assert.Equal(t, "--no-pager --json=short list 4242\n", string(called))

	fakeCoredumpctl(t, `[{"time": `+coredumpctlTime(crashAt)+`, "pid": 4242,
		"corefile": "missing"}]`)

	_, err = testLocator(t, pattern, false).Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "coredumpctl has the crash of the PID 4242, and its core "+
		"is missing")

	fakeCoredumpctl(t, `[{"time": `+coredumpctlTime(crashAt.Add(-time.Hour))+`, "pid": 4242,
		"corefile": "present"}]`)

	_, err = testLocator(t, pattern, false).Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "coredumpctl has no core of the PID 4242 of "+
		crashAt.Format(time.DateTime))

	fakeCoredumpctl(t, "")

	_, err = testLocator(t, pattern, false).Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "coredumpctl has no core of the PID 4242")

	fakeCoredumpctl(t, "not json")

	_, err = testLocator(t, pattern, false).Locate(t.Context(), inst, testCrash)
	require.ErrorContains(t, err, "cannot read what coredumpctl --json=short list 4242 prints")

	// No coredumpctl at all.
	t.Setenv("PATH", t.TempDir())

	_, err = testLocator(t, pattern, false).Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, exec.ErrNotFound)
	require.ErrorContains(t, err, "pipes the cores to systemd-coredump, and coredumpctl is "+
		"not found")
}

// TestLocatePipe finds the core and the archive tt coredump pack, the pipe
// of core_pattern, made in its --dir, by the time of the dump in their
// names.
func TestLocatePipe(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir()}
	loc := testLocator(t, "|/usr/bin/tt coredump pack -d "+dir+" -p %p -t %t", false)
	dumped := crashAt.Add(-3 * time.Second)

	_, err := loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "pipes the cores to tt coredump pack, and "+dir+
		" has neither the core of the PID 4242 nor its archive")

	archive := filepath.Join(dir, "tarantool-core-4242-"+dumped.Format(archiveTimeLayout)+
		"-host.tar.gz")
	writeDumped(t, archive, 100, crashAt.Add(time.Minute))
	// The archive of an earlier crash of the PID.
	writeDumped(t, filepath.Join(dir, "tarantool-core-4242-"+
		crashAt.Add(-time.Hour).Format(archiveTimeLayout)+"-host.tar.gz"), 1, crashAt)

	core, err := loc.Locate(t.Context(), inst, testCrash)
	require.NoError(t, err)
	assert.Equal(t, SourceArchive, core.Source)
	assert.Equal(t, archive, core.Path)

	stored := filepath.Join(dir, "tarantool-core.4242."+strconv.FormatInt(dumped.Unix(), 10))
	writeDumped(t, stored, 4096, crashAt.Add(time.Minute))

	core, err = loc.Locate(t.Context(), inst, testCrash)
	require.NoError(t, err)
	assert.Equal(t, Core{
		Source: SourcePipe, Path: stored, Size: 4096, Archive: archive, Time: dumped,
	}, core)

	// A core of the name of another process is not the one of the crash.
	writeCoreOf(t, stored, 4243, crashAt.Add(time.Minute))

	_, err = loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrOtherProcess)
	require.ErrorContains(t, err, stored+", written at "+dumped.Format(time.DateTime)+", is the "+
		"core of the PID 4243")

	for _, args := range [][]string{
		{"coredump", "pack", "-d", "/var/core", "-p", "%p"},
		{"coredump", "pack", "--dir", "/var/core"},
		{"coredump", "pack", "--dir=/var/core"},
		{"coredump", "pack", "-d/var/core"},
	} {
		assert.Equal(t, "/var/core", packDir(args), args)
	}

	assert.Equal(t, "/", packDir([]string{"coredump", "pack", "-p", "%p"}))
	assert.Equal(t, "/var/core", packDir([]string{"-d", "var/core"}), "relative to the root")
	assert.True(t, isPackPipe([]string{"-v", "coredump", "pack"}))
	assert.False(t, isPackPipe([]string{"pack", "coredump"}))
}

// apportPattern is kernel.core_pattern of apport.
const apportPattern = "|/usr/share/apport/apport -p%p -s%s -c%c -d%d -P%P -u%u -g%g -F%F -- %E"

// TestLocateApport finds the core apport keeps in its directory of the
// cores, core.<executable>.<UID>.<boot ID>.<PID>.<start time>, of this boot
// and of the time, or, of an older apport, in the working directory of the
// process, and names a report of the time in /var/crash without one.
func TestLocateApport(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: work}
	loc := testLocator(t, apportPattern, true)

	_, err := loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.EqualError(t, err, "the core is not found: kernel.core_pattern pipes the cores to "+
		"apport, which kept no core of the PID 4242 in "+loc.apportDir+", nor "+
		filepath.Join(work, "core.4242")+", and wrote no report into "+loc.crashDir)

	report := filepath.Join(loc.crashDir, "_usr_bin_tarantool.1000.crash")
	writeDumped(t, report, 10, crashAt.Add(time.Second))

	_, err = loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "apport may have reported the crash in "+report+
		": apport-unpack it")

	// The cores apport keeps of other boots, PIDs and times are not it.
	name := func(boot, pid, start string) string {
		return filepath.Join(loc.apportDir, "core._opt_tarantool-3_9_0_bin_tarantool.1000."+
			boot+"."+pid+"."+start)
	}

	writeDumped(t, name("ffffffff-0000-0000-0000-000000000000", "4242", "77"), 1, crashAt)
	writeDumped(t, name(testBootID, "14242", "77"), 2, crashAt)
	writeDumped(t, name(testBootID, "4242", "11"), 3, crashAt.Add(-time.Hour))

	require.NoError(t, os.Remove(report))

	_, err = loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "which kept no core of the PID 4242 in "+loc.apportDir+
		" of the time of the crash ("+filepath.Base(name(testBootID, "4242", "11"))+
		" is of another), nor ")

	// The legacy core of the working directory is taken without one there.
	legacy := filepath.Join(work, "core.4242")
	writeDumped(t, legacy, 4096, crashAt.Add(time.Second))

	core, err := loc.Locate(t.Context(), inst, testCrash)
	require.NoError(t, err)
	assert.Equal(t, SourceApport, core.Source)
	assert.Equal(t, legacy, core.Path)

	// The core apport keeps is taken first; SIGQUIT goes there as well.
	kept := name(testBootID, "4242", "1234")
	writeCoreOf(t, kept, 4242, crashAt.Add(time.Second))

	quit := sdk.Crash{At: testCrash.At, PID: testCrash.PID, Signal: syscall.SIGQUIT,
		CoreDumped: true}

	for _, crash := range []sdk.Crash{testCrash, quit} {
		core, err = loc.Locate(t.Context(), inst, crash)
		require.NoError(t, err)
		assert.Equal(t, Core{
			Source: SourceApport, Path: kept, Size: core.Size, Archive: "",
			Time: crashAt.Add(time.Second),
		}, core)
	}

	// One of another process of the PID is not it, nor is the legacy core.
	writeCoreOf(t, kept, 4243, crashAt.Add(time.Second))

	_, err = loc.Locate(t.Context(), inst, testCrash)
	require.ErrorIs(t, err, ErrOtherProcess)
	require.ErrorContains(t, err, kept+", written at ")
}

// TestNewestFirst pins the order of the crashes of a list.
func TestNewestFirst(t *testing.T) {
	t.Parallel()

	crashes := []sdk.Crash{
		{At: crashAt, PID: 1}, {At: crashAt.Add(time.Hour), PID: 2},
		{At: crashAt.Add(-time.Hour), PID: 3},
	}

	sorted := NewestFirst(crashes)
	assert.Equal(t, []int{2, 1, 3}, []int{sorted[0].PID, sorted[1].PID, sorted[2].PID})
	assert.Equal(t, 1, crashes[0].PID, "the crashes given stay as they are")
}

// TestList lists the crashes of instances with where their cores are, and
// prints the table.
func TestList(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	path := filepath.Join(work, "core.4242")
	writeDumped(t, path, 1536, crashAt.Add(-time.Second))

	killed := sdk.Crash{
		At: crashAt.Add(-time.Hour), PID: 4000, Signal: syscall.SIGKILL, CoreDumped: false,
	}
	gone := sdk.Crash{
		At: crashAt.Add(-2 * time.Hour), PID: 3000, Signal: syscall.SIGABRT, CoreDumped: true,
	}
	instances := []sdk.LocalInstance{
		{Name: "app:i1", WorkDir: work, Crashes: []sdk.Crash{gone, testCrash, killed}},
		{Name: "app:i2", WorkDir: work},
	}

	list := List(context.Background(), testLocator(t, "core", true), instances)
	require.Len(t, list, 2)
	assert.Equal(t, []string{"app:i1"}, list.Crashed())
	assert.Equal(t, []string{"app:i2"}, list.NotCrashed())
	assert.True(t, list.HasCore())
	assert.Empty(t, list[1].Crashes)

	crashes := list[0].Crashes
	require.Len(t, crashes, 3)
	assert.Equal(t, ListedCrash{
		At: crashAt, PID: 4242, Signal: 11, SignalName: "SIGSEGV", CoreDumped: true,
		Core: &ListedCore{Source: SourceFile, Path: path, Size: 1536, Archive: ""}, Missing: "",
	}, crashes[0])
	assert.Equal(t, 4000, crashes[1].PID)
	assert.Nil(t, crashes[1].Core)
	assert.Contains(t, crashes[1].Missing, "no core was dumped")
	assert.Contains(t, crashes[2].Missing, "no file matches")

	var out strings.Builder

	require.NoError(t, list.Human(&out))

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, 4, out.String())
	assert.Regexp(t, `^INSTANCE  CRASHED AT\s+PID\s+SIGNAL\s+CORE$`, lines[0])
	assert.Regexp(t, `^app:i1\s+`+crashAt.Format(time.DateTime)+`\s+4242\s+SIGSEGV\s+`+
		path+`, 1.5 KiB$`, lines[1])
	assert.Regexp(t, `\s+4000\s+SIGKILL\s+none dumped$`, lines[2])
	assert.Regexp(t, `\s+3000\s+SIGABRT\s+not found: no file matches `+
		filepath.Join(work, "core.3000"), lines[3])

	out.Reset()
	require.NoError(t, List(context.Background(), testLocator(t, "core", true),
		instances[1:]).Human(&out))
	assert.Empty(t, out.String(), "no crash, no table")

	// Crashes that cannot be read are no crashes of the instance, and no
	// lack of them either; the core of another process is not found.
	other := filepath.Join(t.TempDir(), "core.4242")
	writeCoreOf(t, other, 4243, crashAt.Add(-time.Second))

	list = List(context.Background(), testLocator(t, other, false), []sdk.LocalInstance{
		{Name: "app:i3", CrashesErr: errors.New("cannot read the crashes: permission denied")},
		{Name: "app:i4", Crashes: []sdk.Crash{testCrash}},
	})
	assert.Equal(t, []ListedInstance{list[0]}, list.Unreadable())
	assert.Equal(t, "cannot read the crashes: permission denied", list[0].CrashesError)
	assert.Empty(t, list.NotCrashed())
	assert.Equal(t, []string{"app:i4"}, list.Crashed())
	assert.False(t, list.HasCore())

	out.Reset()
	require.NoError(t, list.Human(&out))
	assert.Contains(t, out.String(), "4242  SIGSEGV  not found: it is the core of another "+
		"process: "+other+", written at ")
	assert.NotContains(t, out.String(), "app:i3")
}

// TestFormatSize pins the sizes the table and pack tell: a tenth of the
// unit, and the next unit for a value that rounds to 1024.0 of one.
func TestFormatSize(t *testing.T) {
	t.Parallel()

	for size, want := range map[int64]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB",
		1048524: "1023.9 KiB", 1048525: "1.0 MiB", 1048556: "1.0 MiB", 1 << 20: "1.0 MiB",
		3 << 30: "3.0 GiB", 1<<40 - 1: "1.0 TiB", 1 << 50: "1.0 PiB",
	} {
		assert.Equal(t, want, FormatSize(size), size)
	}
}

// TestDescribeCore pins where the table says a core is.
func TestDescribeCore(t *testing.T) {
	t.Parallel()

	crash := ListedCrash{PID: 4242, CoreDumped: true}

	crash.Core = &ListedCore{Source: SourceCoredumpctl, Size: 2 << 20}
	assert.Equal(t, "kept by systemd-coredump: coredumpctl dump 4242, 2.0 MiB",
		describeCore(crash))

	crash.Core = &ListedCore{Source: SourceCoredumpctl}
	assert.Equal(t, "kept by systemd-coredump: coredumpctl dump 4242", describeCore(crash))

	crash.Core = &ListedCore{Source: SourceArchive, Path: "/var/core/a.tar.gz", Size: 10}
	assert.Equal(t, "/var/core/a.tar.gz, 10 B, packed by the core_pattern pipe",
		describeCore(crash))

	crash.Core = &ListedCore{Source: SourcePipe, Path: "/var/core/c", Size: 3 << 30,
		Archive: "/var/core/a.tar.gz"}
	assert.Equal(t, "/var/core/c, 3.0 GiB, packed into /var/core/a.tar.gz", describeCore(crash))
}

// TestSignalName pins the names of the signals.
func TestSignalName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "SIGSEGV", SignalName(syscall.SIGSEGV))
	assert.Equal(t, "SIGQUIT", SignalName(syscall.SIGQUIT))
	assert.Equal(t, "signal 64", SignalName(syscall.Signal(64)))
}
