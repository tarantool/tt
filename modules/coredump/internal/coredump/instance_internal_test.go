package coredump

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// TestLastCore pins which crash of an instance pack takes: the last one
// that dumped a core, with the later ones that dumped none, and why there is
// none.
func TestLastCore(t *testing.T) {
	t.Parallel()

	work := t.TempDir()
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: work}
	loc := testLocator(t, "core", true)

	_, err := loc.LastCore(t.Context(), inst)
	require.ErrorIs(t, err, ErrNoCrash)
	require.ErrorContains(t, err, "app:i1: no crash is recorded")

	killed := sdk.Crash{
		At: crashAt.Add(time.Minute), PID: 4300, Signal: syscall.SIGKILL, CoreDumped: false,
	}

	inst.Crashes = []sdk.Crash{killed}

	_, err = loc.LastCore(t.Context(), inst)
	require.ErrorIs(t, err, ErrNoCoreDumped)
	require.ErrorContains(t, err, "app:i1: no core was dumped by any of its 1 crashes: the "+
		"last, PID 4300, SIGKILL, at "+killed.At.Format(time.DateTime))

	inst.Crashes = []sdk.Crash{testCrash, killed}

	_, err = loc.LastCore(t.Context(), inst)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "the core of the crash of app:i1, PID 4242, SIGSEGV, at "+
		crashAt.Format(time.DateTime)+": the core is not found: no file matches ")

	path := filepath.Join(work, "core.4242")
	writeDumped(t, path, 10, crashAt)

	found, err := loc.LastCore(t.Context(), inst)
	require.NoError(t, err)
	assert.Equal(t, testCrash, found.Crash)
	assert.Equal(t, path, found.Core.Path)
	assert.Equal(t, []sdk.Crash{killed}, found.Later)

	core, remove, err := loc.Fetch(t.Context(), found, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, path, core, "a core file is packed where it is")
	remove()
	assert.FileExists(t, path, "and stays")

	// The core of another process at the path is not packed.
	writeCoreOf(t, path, 4243, crashAt)

	_, err = loc.LastCore(t.Context(), inst)
	require.ErrorIs(t, err, ErrOtherProcess)
	require.ErrorContains(t, err, "the core of the crash of app:i1, PID 4242, SIGSEGV, at "+
		crashAt.Format(time.DateTime)+": the core is not found: it is the core of another "+
		"process: "+path)

	// Crashes that cannot be read are no crash, and say why.
	unreadable := sdk.LocalInstance{Name: "app:i2", CrashesErr: errors.New("cannot read the " +
		"crashes: permission denied")}

	_, err = loc.LastCore(t.Context(), unreadable)
	require.EqualError(t, err, "app:i2: cannot read the crashes: permission denied")
	require.NotErrorIs(t, err, ErrNoCrash)

	writeDumped(t, path, 10, crashAt)

	// The core_pattern pipe packed it already, and its core is gone.
	dir := t.TempDir()
	archive := filepath.Join(dir, "tarantool-core-4242-"+crashAt.Format(archiveTimeLayout)+
		"-host.tar.gz")
	writeDumped(t, archive, 10, crashAt)

	_, err = testLocator(t, "|/usr/bin/tt coredump pack -d "+dir+" -p %p -t %t", false).
		LastCore(t.Context(), inst)
	require.ErrorIs(t, err, ErrPackedByPipe)
	require.ErrorContains(t, err, "the core is packed already, by the kernel.core_pattern "+
		"pipe: "+archive)
}

// TestIsInstanceTarget pins the COREDUMP of pack that names an instance: no
// file, <app>:<instance> or <app>:*.
func TestIsInstanceTarget(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, os.WriteFile("app:file", nil, 0o600))

	for arg, want := range map[string]bool{
		"app:i1": true, "app:*": true, "core": false, "app:file": false, "app:": false,
		":i1": false, "./app:i1": false, "/var/core/app:i1": false, "-": false,
		"app:i1,app:i2": false,
	} {
		assert.Equal(t, want, IsInstanceTarget(arg), arg)
	}
}

// fakeTimestamp is COREDUMP_TIMESTAMP of the entry of testCrash fakeDump
// lists: the time of the dump, a second before the entry.
var fakeTimestamp = strconv.FormatInt(crashAt.Add(-2*time.Second).Truncate(time.Second).
	UnixMicro(), 10)

// fakeDump makes coredumpctl, a fake of PATH, list one core of testCrash,
// tell its COREDUMP_TIMESTAMP, fakeTimestamp, of --field with --since and
// --until of the time of the entry, and dump it to its standard output as
// the content of core, or as nothing when core is empty, of the matches of
// the PID and the timestamp; before it dumps, it touches the file ready and
// runs sleep, when it is not empty, as long as a dump that is stopped. It
// returns the file of the arguments of its runs.
func fakeDump(t *testing.T, core, sleep string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	ready := filepath.Join(dir, "ready")
	listed := crashAt.Add(-time.Second)
	when := fmt.Sprintf("@%d.%06d", listed.Unix(), listed.Nanosecond()/1000)
	list := `[{"time": ` + coredumpctlTime(listed) + `, "pid": 4242,
		"sig": 11, "corefile": "present", "size": 4096}]`
	script := "#!/bin/sh\necho \"$@\" >> " + args + "\n" +
		"case \"$*\" in\n" +
		"'--no-pager --field=COREDUMP_TIMESTAMP --since=" + when + " --until=" + when +
		" list 4242') echo " + fakeTimestamp + ";;\n" +
		"*' list '*) cat <<'EOF'\n" + list + "\nEOF\n;;\n" +
		"'--no-pager dump COREDUMP_PID=4242 COREDUMP_TIMESTAMP=" + fakeTimestamp + "')\n" +
		"  echo 'PID: 4242' >&2\n"

	if core != "" {
		script += "  head -c 4096 " + core + "\n  touch " + ready + "\n"
		if sleep != "" {
			script += "  exec sleep " + sleep + "\n"
		}

		script += "  tail -c +4097 " + core + "\n"
	}

	script += ";;\n*) echo 'No match found.' >&2; exit 1;;\nesac\n"

	require.NoError(t, os.WriteFile(filepath.Join(dir, coredumpctlName), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return args, ready
}

// sparseContent is a core of a megabyte of zeros, data in its first and its
// last block, as a core of a pipe is mostly zeros.
func sparseContent() []byte {
	content := make([]byte, 1<<20)

	copy(content, "the core")
	copy(content[len(content)-8:], "its end.")

	return content
}

// TestFetchCoredumpctl dumps the core of a crash systemd-coredump keeps with
// coredumpctl, a fake of PATH, into a file of the directory of the archive,
// which goes once it is packed, and leaves nothing of one it cannot dump.
// The entry dumped is pinned by its PID and its COREDUMP_TIMESTAMP, which
// list tells of the entry of the time: dump takes the last entry its
// matches select, whatever --since and --until say. The core comes through
// the standard output of coredumpctl and keeps its holes, where the file
// system keeps a hole of their size (see keepsHoles).
//
//nolint:paralleltest // Sets PATH.
func TestFetchCoredumpctl(t *testing.T) {
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir(), Crashes: []sdk.Crash{
		testCrash,
	}}
	pattern := "|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h"
	source := filepath.Join(t.TempDir(), "core")

	require.NoError(t, os.WriteFile(source, sparseContent(), 0o600))

	args, _ := fakeDump(t, source, "")
	loc := testLocator(t, pattern, false)

	found, err := loc.LastCore(t.Context(), inst)
	require.NoError(t, err)
	assert.Equal(t, SourceCoredumpctl, found.Core.Source)

	dir := t.TempDir()

	core, remove, err := loc.Fetch(t.Context(), found, dir)
	require.NoError(t, err)
	assert.Equal(t, dir, filepath.Dir(core))
	assert.True(t, strings.HasPrefix(filepath.Base(core), ".tt-coredump-4242-"), core)

	content, err := os.ReadFile(core)
	require.NoError(t, err)
	assert.Equal(t, sparseContent(), content)

	info, err := os.Stat(core)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// The zeros between the blocks of data are a hole: a file system that
	// keeps a hole of them, as those of Linux do, allocates the two blocks
	// alone.
	if keepsHoles(t, dir, int64(len(sparseContent())-2*sparseBlock)) {
		assert.Less(t, diskSize(t, core), int64(256<<10), "the core is written sparse")
	}

	called, err := os.ReadFile(args)
	require.NoError(t, err)

	listed := crashAt.Add(-time.Second)
	when := fmt.Sprintf("@%d.%06d", listed.Unix(), listed.Nanosecond()/1000)
	assert.Equal(t, "--no-pager --json=short list 4242\n"+
		"--no-pager --field=COREDUMP_TIMESTAMP --since="+when+" --until="+when+" list 4242\n"+
		"--no-pager dump COREDUMP_PID=4242 COREDUMP_TIMESTAMP="+fakeTimestamp+"\n",
		string(called))

	remove()
	assert.NoFileExists(t, core)

	// A dump that writes nothing leaves nothing.
	fakeDump(t, "", "")

	_, _, err = loc.Fetch(t.Context(), found, dir)
	require.ErrorIs(t, err, ErrCoreNotFound)
	require.ErrorContains(t, err, "coredumpctl dump of the PID 4242 wrote no core into ")

	// Nor does one of the core of another process.
	other := testCore{segments: nil, mappings: nil, auxv: nil, notes: prpsinfoNote(4243)}.write(t,
		filepath.Join(t.TempDir(), "core"))

	fakeDump(t, other, "")

	_, _, err = loc.Fetch(t.Context(), found, dir)
	require.ErrorIs(t, err, ErrOtherProcess)
	require.ErrorContains(t, err, "coredumpctl dumped the core of the PID 4243, not of the PID "+
		"4242 of the crash")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)

	// An entry list tells no COREDUMP_TIMESTAMP of is not dumped: it cannot
	// be told from another of the PID.
	moved := found

	moved.Core.Time = moved.Core.Time.Add(time.Microsecond)

	_, _, err = loc.Fetch(t.Context(), moved, dir)
	require.ErrorContains(t, err, "cannot tell the core of the crash from the other cores of "+
		"the PID 4242 coredumpctl keeps")
}

// TestFetchCoredumpctlStopped stops the dump of coredumpctl, a fake of PATH
// that sleeps once it has written a block, as a signal to tt stops it: the
// file of the core goes, and the error is the cause of the stop.
//
//nolint:paralleltest // Sets PATH.
func TestFetchCoredumpctlStopped(t *testing.T) {
	source := filepath.Join(t.TempDir(), "core")

	require.NoError(t, os.WriteFile(source, sparseContent(), 0o600))

	_, ready := fakeDump(t, source, "60")
	loc := testLocator(t, "|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h", false)
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: t.TempDir(), Crashes: []sdk.Crash{
		testCrash,
	}}

	found, err := loc.LastCore(t.Context(), inst)
	require.NoError(t, err)

	dir := t.TempDir()
	stopped := errors.New("interrupted by SIGTERM")
	ctx, cancel := context.WithCancelCause(t.Context())

	go func() {
		for {
			_, err := os.Stat(ready)
			if err == nil {
				break
			}

			time.Sleep(10 * time.Millisecond)
		}

		cancel(stopped)
	}()

	began := time.Now()

	_, _, err = loc.Fetch(ctx, found, dir)
	require.ErrorIs(t, err, stopped)
	assert.Less(t, time.Since(began), 30*time.Second, "the dump is stopped, not waited for")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the core begun is removed")
}

// TestPackCoredumpctlCore runs the whole of pack by an instance whose core
// systemd-coredump keeps: coredumpctl, a fake of PATH, dumps a core of a
// tarantool cc built, Pack packs it into an archive named by the PID, and
// the dumped core goes.
//
//nolint:paralleltest // Sets PATH.
func TestPackCoredumpctlCore(t *testing.T) {
	requirePackScript(t)

	dir := t.TempDir()
	tarantool := compileTarantool(t, dir)
	built := binaryCore(t, tarantool, 0x555555554000)
	source := built.write(t, filepath.Join(dir, "core"))

	fakeDump(t, source, "")

	loc := testLocator(t, "|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h", false)
	inst := sdk.LocalInstance{Name: "app:i1", WorkDir: dir, Crashes: []sdk.Crash{testCrash}}

	found, err := loc.LastCore(t.Context(), inst)
	require.NoError(t, err)

	out := t.TempDir()

	core, remove, err := loc.Fetch(t.Context(), found, out)
	require.NoError(t, err)

	log, _ := logtest.New(t)

	archive, _, err := Pack(t.Context(), output.Streams{In: nil, Out: io.Discard, Err: io.Discard},
		log, PackOptions{
			Core: core, Executable: tarantool, Tarantools: nil, Dir: out, PID: 4242,
			Time: strconv.FormatInt(crashAt.Unix(), 10),
		})
	require.NoError(t, err)
	remove()

	assert.Equal(t, out, filepath.Dir(archive))
	assert.True(t, strings.HasPrefix(filepath.Base(archive),
		"tarantool-core-4242-"+crashAt.Format(archiveTimeLayout)+"-"), archive)

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the archive alone")
	assert.Equal(t, filepath.Base(archive), entries[0].Name())
}
