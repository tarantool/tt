package coredump

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// gdbWithoutPython is how gdb 11.2 built without Python answers the probe
// of inspect: show version prints, and the python command fails, gdb
// exiting with 1.
const gdbWithoutPython = "echo 'GNU gdb (GDB) 11.2'; " +
	"echo 'Python scripting is not supported in this copy of GDB.' >&2; exit 1"

// gdbOld is how gdb 11.2 with Python answers the probe.
const gdbOld = "echo 'GNU gdb (GDB) 11.2'; echo 'tt-gdb-python: 11.2'"

func TestParseGDBProbe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		out   string
		probe debuggerProbe
		ok    bool
	}{
		{
			name: "python",
			out: "GNU gdb (Ubuntu 17.1-2ubuntu1) 17.1\nCopyright (C) 2025 Free Software " +
				"Foundation, Inc.\ntt-gdb-python: 17.1\n",
			probe: debuggerProbe{Path: "", Version: debuggerVersion{17, 1}, Python: true},
			ok:    true,
		},
		{
			name:  "no python",
			out:   "GNU gdb (GDB) 11.2\nCopyright (C) 2022 Free Software Foundation, Inc.\n",
			probe: debuggerProbe{Path: "", Version: debuggerVersion{11, 2}, Python: false},
			ok:    true,
		},
		{
			name:  "a version of a distribution",
			out:   "GNU gdb (Fedora Linux) 16.3-1.fc42\ntt-gdb-python: 16.3-1.fc42\n",
			probe: debuggerProbe{Path: "", Version: debuggerVersion{16, 3}, Python: true},
			ok:    true,
		},
		{
			name: "a snapshot",
			out:  "GNU gdb (GDB) 17.0.50.20251101-git\ntt-gdb-python: 17.0.50.20251101-git\n",
			probe: debuggerProbe{
				Path: "", Version: debuggerVersion{17, 0, 50}, Python: true,
			},
			ok: true,
		},
		{
			// The gdb Ubuntu 24.04 was released with, a snapshot before
			// 15.1, which its updates bring.
			name: "the snapshot of a distribution",
			out: "GNU gdb (Ubuntu 15.0.50.20240403-0ubuntu1) 15.0.50.20240403-git\n" +
				"tt-gdb-python: 15.0.50.20240403-git\n",
			probe: debuggerProbe{
				Path: "", Version: debuggerVersion{15, 0, 50}, Python: true,
			},
			ok: true,
		},
		{
			name: "a pre-release",
			out:  "GNU gdb (GDB) 17.0.90.20251004-git\n",
			probe: debuggerProbe{
				Path: "", Version: debuggerVersion{17, 0, 90}, Python: false,
			},
			ok: true,
		},
		{
			// gdb.VERSION tells nothing: show version does.
			name:  "the version of show version",
			out:   "GNU gdb (GDB) 12.1\ntt-gdb-python: unknown\n",
			probe: debuggerProbe{Path: "", Version: debuggerVersion{12, 1}, Python: true},
			ok:    true,
		},
		{
			name:  "no version",
			out:   "GNU gdb (GDB) unknown\n",
			probe: debuggerProbe{Path: "", Version: debuggerVersion{}, Python: false},
			ok:    true,
		},
		{
			name:  "no gdb",
			out:   "Segmentation fault\n",
			probe: debuggerProbe{Path: "", Version: debuggerVersion{}, Python: false},
			ok:    false,
		},
		{
			name:  "nothing",
			out:   "",
			probe: debuggerProbe{Path: "", Version: debuggerVersion{}, Python: false},
			ok:    false,
		},
	}

	for _, testCase := range cases {
		probe, ok := parseGDBProbe(testCase.out)
		assert.Equal(t, testCase.ok, ok, testCase.name)
		assert.Equal(t, testCase.probe, probe, testCase.name)
	}
}

// TestDebuggerVersion checks that a version of gdb is told and named with
// its patch, and that a snapshot or a pre-release of x.1, x.0.50 or
// x.0.90, comes before x.1 and after the releases of x-1.
func TestDebuggerVersion(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]debuggerVersion{
		"17.1":                 {17, 1, 0},
		"15.1-1.fc41":          {15, 1, 0},
		"17.0.50.20251101-git": {17, 0, 50},
		"15.0.50.20240403-git": {15, 0, 50},
		"8.0.1":                {8, 0, 1},
	} {
		version, ok := parseDebuggerVersion(text)
		require.True(t, ok, text)
		assert.Equal(t, want, version, text)
	}

	_, ok := parseDebuggerVersion("unknown")
	assert.False(t, ok)

	assert.Equal(t, "17.1", debuggerVersion{17, 1, 0}.String())
	assert.Equal(t, "17.0.50", debuggerVersion{17, 0, 50}.String())

	ordered := []debuggerVersion{{16, 3, 0}, {17, 0, 50}, {17, 0, 90}, {17, 1, 0}, {17, 2, 0}}
	for index := 1; index < len(ordered); index++ {
		assert.Negative(t, ordered[index-1].compare(ordered[index]), ordered[index])
	}
}

// withGDB puts a gdb that runs the shell commands script first in PATH, and
// returns its path.
func withGDB(t *testing.T, script string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake gdb is a shell script")
	}

	bin := t.TempDir()
	path := filepath.Join(bin, debuggerGDB)
	require.NoError(t, execfile.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return path
}

// TestGDBProbe checks the probe of the gdb of PATH: its version and its
// Python, from what it prints, whatever its exit status; a gdb that prints
// neither, or does not answer in time, tells nothing, and no gdb in PATH is
// an error of its own.
//
//nolint:paralleltest // Sets PATH.
func TestGDBProbe(t *testing.T) {
	t.Run("python", func(t *testing.T) {
		path := withGDB(t, gdbWithPython)

		probe, err := gdbDebugger{}.Probe(t.Context())
		require.NoError(t, err)
		assert.Equal(t, debuggerProbe{Path: path, Version: debuggerVersion{17, 1}, Python: true},
			probe)
	})

	t.Run("no python", func(t *testing.T) {
		path := withGDB(t, gdbWithoutPython)

		probe, err := gdbDebugger{}.Probe(t.Context())
		require.NoError(t, err)
		assert.Equal(t, debuggerProbe{Path: path, Version: debuggerVersion{11, 2}, Python: false},
			probe)
	})

	t.Run("nothing", func(t *testing.T) {
		withGDB(t, "echo 'Segmentation fault'; exit 139")

		_, err := gdbDebugger{}.Probe(t.Context())
		require.ErrorIs(t, err, errProbeFailed)
		require.ErrorContains(t, err, "Segmentation fault")
	})

	t.Run("no answer", func(t *testing.T) {
		withGDB(t, "exec sleep 30")

		timeout := gdbProbeTimeout

		gdbProbeTimeout = 100 * time.Millisecond

		t.Cleanup(func() { gdbProbeTimeout = timeout })

		start := time.Now()
		_, err := gdbDebugger{}.Probe(t.Context())
		require.ErrorIs(t, err, errProbeFailed)
		assert.Less(t, time.Since(start), 10*time.Second)
	})

	// A gdb stopped after show version, before its Python answered, tells
	// nothing of its Python: it is no gdb without one. The probe is stopped
	// once the fake has printed its version, whenever that is: the fake
	// opens a FIFO to write after it, which the test waits on, and the
	// timeout is out of the way.
	t.Run("stopped after the version", func(t *testing.T) {
		printed := filepath.Join(t.TempDir(), "printed")
		withGDB(t, "echo 'GNU gdb (GDB) 15.1'; echo printed >'"+printed+"'; exec sleep 30")
		require.NoError(t, exec.CommandContext(t.Context(), "mkfifo", printed).Run())

		timeout := gdbProbeTimeout

		gdbProbeTimeout = time.Hour

		t.Cleanup(func() { gdbProbeTimeout = timeout })

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go func() {
			defer cancel()

			// The open returns once the fake opens the FIFO to write.
			fifo, err := os.Open(printed)
			if err == nil {
				_, _ = io.Copy(io.Discard, fifo)
				_ = fifo.Close()
			}
		}()

		_, err := gdbDebugger{}.Probe(ctx)

		// A fake that exited before it opened the FIFO leaves the open
		// waiting: a writer that opens it and goes ends it.
		writer, openErr := os.OpenFile(printed, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if openErr == nil {
			_ = writer.Close()
		}

		require.ErrorIs(t, err, errProbeFailed)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorContains(t, err, "GNU gdb (GDB) 15.1")
	})

	t.Run("no gdb", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		_, err := gdbDebugger{}.Probe(t.Context())
		require.ErrorIs(t, err, errNoDebugger)
		require.ErrorIs(t, err, exec.ErrNotFound)
		require.ErrorContains(t, err, "gdb is not found in PATH: install gdb")
	})
}

// TestWarnDebugger checks what is warned of a debugger by its probe: one
// without Python, whatever its version, and with the extensions of
// tarantool-gdb-tools a gdb older than the oldest they are checked on.
func TestWarnDebugger(t *testing.T) {
	t.Parallel()

	noPython := []string{
		"gdb 11.2 at /usr/bin/gdb has no Python: no extension will load in it, and neither the " +
			"lj-* commands of LuaJIT nor the tt-* commands of Tarantool will exist, only plain gdb",
		"hint: install a gdb built with Python, or put one first in PATH",
	}
	old := []string{
		"gdb 11.2 at /usr/bin/gdb is older than the gdb the extensions of tarantool-gdb-tools " +
			"are checked on, 12.1, 15.1, 17.1 and 18.1: they may not work in it",
		"hint: use gdb 12.1 or later",
	}

	probe := func(major, minor int, python bool) debuggerProbe {
		return debuggerProbe{
			Path: "/usr/bin/gdb", Version: debuggerVersion{major, minor, 0}, Python: python,
		}
	}

	// A snapshot before 12.1 is older than it, the one before 15.1 Ubuntu
	// 24.04 was released with is not.
	snapshot := func(major int) debuggerProbe {
		return debuggerProbe{
			Path: "/usr/bin/gdb", Version: debuggerVersion{major, 0, 50}, Python: true,
		}
	}
	oldSnapshot := []string{
		"gdb 12.0.50 at /usr/bin/gdb is older than the gdb the extensions of " +
			"tarantool-gdb-tools are checked on, 12.1, 15.1, 17.1 and 18.1: they may not work in " +
			"it",
		"hint: use gdb 12.1 or later",
	}

	cases := []struct {
		name     string
		debugger string
		probe    debuggerProbe
		gdbTools bool
		warnings []string
	}{
		{"python", debuggerGDB, probe(17, 1, true), true, nil},
		{"no python", debuggerGDB, probe(11, 2, false), false, noPython},
		{"no python, gdb-tools", debuggerGDB, probe(11, 2, false), true, noPython},
		{"old, gdb-tools", debuggerGDB, probe(11, 2, true), true, old},
		{"old, upstream", debuggerGDB, probe(11, 2, true), false, nil},
		{"the oldest checked", debuggerGDB, probe(12, 1, true), true, nil},
		{"between the checked", debuggerGDB, probe(13, 2, true), true, nil},
		{"a snapshot before the oldest", debuggerGDB, snapshot(12), true, oldSnapshot},
		{"a snapshot after the oldest", debuggerGDB, snapshot(15), true, nil},
		{"unknown version", debuggerGDB, probe(0, 0, true), true, nil},
		{"lldb", debuggerLLDB, probe(11, 2, true), true, nil},
	}

	for _, testCase := range cases {
		log, recorder := logtest.New(t)

		warnDebugger(log, testCase.debugger, testCase.probe, testCase.gdbTools)
		assert.Equal(t, testCase.warnings, warnings(recorder), testCase.name)
	}
}

// TestInspectProbesGDB checks that inspect probes the gdb of PATH before it
// runs it through gdb.sh and warns of what it lacks: Python, with the
// extensions of the Tarantool of the core or those of tarantool-gdb-tools,
// and, with the latter alone, a version older than they are checked on.
//
//nolint:paralleltest // Sets PATH.
func TestInspectProbesGDB(t *testing.T) {
	dir := t.TempDir()
	writeArtefacts(t, dir)

	run := func(t *testing.T, probe string, gdbTools bool) []string {
		t.Helper()

		withFakeGDBProbe(t, probe)

		_, server := newFakeGitHub(t)
		res, recorder := testResolver(t, server, t.TempDir())

		var stdout bytes.Buffer

		err := inspect(t.Context(), output.Streams{
			In: strings.NewReader(""), Out: &stdout, Err: &stdout,
		}, res, InspectOptions{Path: dir, SourceDir: "", GDBTools: gdbTools})
		require.NoError(t, err, stdout.String())
		assert.Contains(t, stdout.String(), "\ncore "+filepath.Join(dir, "coredump")+"\n",
			"gdb.sh runs gdb on the core")

		return warnings(recorder)
	}

	for _, gdbTools := range []bool{false, true} {
		assert.Empty(t, run(t, gdbWithPython, gdbTools))

		got := run(t, gdbWithoutPython, gdbTools)
		require.Len(t, got, 2)
		assert.Contains(t, got[0], "gdb 11.2 at ")
		assert.Contains(t, got[0], " has no Python: no extension will load in it")
		assert.Equal(t, "hint: install a gdb built with Python, or put one first in PATH", got[1])
	}

	got := run(t, gdbOld, true)
	require.Len(t, got, 2)
	assert.Contains(t, got[0], "gdb 11.2 at ")
	assert.Contains(t, got[0], " is older than the gdb the extensions of tarantool-gdb-tools "+
		"are checked on, 12.1, 15.1, 17.1 and 18.1")
	assert.Equal(t, "hint: use gdb 12.1 or later", got[1])

	assert.Empty(t, run(t, gdbOld, false))
}

// TestInspectWithoutGDB checks that inspect on a host without gdb in PATH
// fails with an error of tt that says so, before gdb.sh runs: the wrapper
// said it on the standard output, and tt failed with its exit status.
//
//nolint:paralleltest // Sets PATH.
func TestInspectWithoutGDB(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("gdb.sh needs GNU getopt")
	}

	// PATH has what gdb.sh runs before it looks gdb up, and no gdb.
	bin := t.TempDir()

	for _, tool := range []string{"sh", "basename", "dirname", "cat", "getopt"} {
		path, err := exec.LookPath(tool)
		require.NoError(t, err)
		require.NoError(t, os.Symlink(path, filepath.Join(bin, tool)))
	}

	t.Setenv("PATH", bin)

	dir := t.TempDir()
	writeArtefacts(t, dir)

	_, server := newFakeGitHub(t)
	res, _ := testResolver(t, server, t.TempDir())

	var stdout bytes.Buffer

	err := inspect(t.Context(), output.Streams{
		In: strings.NewReader(""), Out: &stdout, Err: &stdout,
	}, res, InspectOptions{Path: dir, SourceDir: "", GDBTools: false})
	require.ErrorContains(t, err, "gdb is not found in PATH: install gdb, or put the directory "+
		"of the gdb to use first in PATH")
	require.ErrorIs(t, err, exec.ErrNotFound)
	assert.Empty(t, stdout.String(), "gdb.sh does not run")
}

// TestInspectProbeFailure checks that inspect runs nothing when the
// debugger is not found, and asks GitHub for no extension first, which may
// wait on it, and runs a debugger whose probe tells nothing, unchecked,
// saying so at the debug level.
func TestInspectProbeFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeArtefacts(t, dir)

	run := func(t *testing.T, backend *recordingDebugger) (*logtest.Recorder, *fakeGitHub, error) {
		t.Helper()

		github, server := newFakeGitHub(t)
		res, recorder := testResolver(t, server, t.TempDir())

		var stdout bytes.Buffer

		err := inspectWith(t.Context(), output.Streams{
			In: strings.NewReader(""), Out: &stdout, Err: &stdout,
		}, res, backend, InspectOptions{Path: dir, SourceDir: "", GDBTools: false})

		return recorder, github, err
	}

	missing := newRecordingDebugger(debuggerGDB)

	missing.probeErr = errNoDebugger

	_, github, err := run(t, missing)
	require.ErrorIs(t, err, errNoDebugger)
	assert.Empty(t, missing.exts, "the debugger is not run")
	assert.Empty(t, github.Requests(), "the extensions are not looked up")

	unknown := newRecordingDebugger(debuggerGDB)

	unknown.probe = debuggerProbe{Path: "", Version: debuggerVersion{}, Python: false}
	unknown.probeErr = errProbeFailed

	recorder, _, err := run(t, unknown)
	require.NoError(t, err)
	assert.Equal(t, []string{luajitDbgName, tarantoolGDBName}, unknown.exts)
	assert.Empty(t, warnings(recorder))
	assert.Contains(t, recorder.Messages(), "cannot tell what gdb can, it is not checked: "+
		errProbeFailed.Error())
}
