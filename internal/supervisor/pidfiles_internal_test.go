package supervisor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPidFiles pins the pid file lifecycle: the supervisor's for the whole
// run, the child's for each child, both in the format tt reads, and neither
// left behind.
func TestPidFiles(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "run", "supervisor.pid")
	childPidFile := filepath.Join(dir, "run", "child.pid")

	// What the pid files held when the Source was asked for a Spec, when a
	// child was reported started and when it was reported exited.
	var pidFileAtNext, childPidFileAtStart, childPidFileAtExit []string

	pidOrNone := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			return "none"
		}

		return string(data)
	}
	spec := helperSpec(t, dir, modeServe)
	src := &testSource{
		next: func(context.Context, int) (Spec, error) {
			pidFileAtNext = append(pidFileAtNext, pidOrNone(pidFile))

			return spec, nil
		},
		restart: func(n int, _ Exit) (bool, error) { return n < 2, nil },
	}
	rec := &recorder{}
	sup := newHarness(t, src, Options{
		PidFile:      pidFile,
		ChildPidFile: childPidFile,
		StopSignals:  stopSignals,
		OnEvent: func(event Event) {
			switch event.(type) {
			case Started:
				childPidFileAtStart = append(childPidFileAtStart, pidOrNone(childPidFile))
			case Exit:
				childPidFileAtExit = append(childPidFileAtExit, pidOrNone(childPidFile))
			}

			rec.record(event)
		},
	})

	sup.rec = rec
	sup.start()

	first := waitEvent[Started](t, rec, 1)
	assert.Equal(t, os.Getpid(), readPid(t, pidFile))
	assert.Equal(t, first.Pid, readPid(t, childPidFile))

	info, err := os.Stat(pidFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	waitReady(t, dir, first.Pid)
	require.NoError(t, syscall.Kill(first.Pid, syscall.SIGKILL))

	second := waitEvent[Started](t, rec, 2)
	assert.Equal(t, second.Pid, readPid(t, childPidFile))
	assert.Equal(t, os.Getpid(), readPid(t, pidFile))

	waitReady(t, dir, second.Pid)
	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())

	assert.NoFileExists(t, pidFile)
	assert.NoFileExists(t, childPidFile)

	self := strconv.Itoa(os.Getpid())
	assert.Equal(t, []string{self, self}, pidFileAtNext)
	assert.Equal(t, []string{strconv.Itoa(first.Pid), strconv.Itoa(second.Pid)},
		childPidFileAtStart)
	assert.Equal(t, []string{"none", "none"}, childPidFileAtExit)

	written, _ := eventsOf[PidFileWritten](rec)
	assert.Equal(t, []PidFileWritten{
		{Path: pidFile, Pid: os.Getpid()},
		{Path: childPidFile, Pid: first.Pid},
		{Path: childPidFile, Pid: second.Pid},
	}, written)
}

// TestPidFileOfLiveProcess pins that the pid file of a live process is
// refused and left alone, before anything runs.
func TestPidFileOfLiveProcess(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "supervisor.pid")
	other := strconv.Itoa(os.Getppid())
	require.NoError(t, os.WriteFile(pidFile, []byte(other), 0o600))

	var cleanups atomic.Int32

	src := fixedSource(helperSpec(t, dir, modeServe), false)
	sup := newHarness(t, src, Options{
		PidFile:     pidFile,
		StopSignals: stopSignals,
		Cleanup:     func() { cleanups.Add(1) },
	})
	sup.start()

	err := sup.wait()
	require.Error(t, err)
	assert.Equal(t, OpPidFile, errorOp(t, err))

	nexts, _ := src.calls()
	assert.Zero(t, nexts)
	assert.Zero(t, cleanups.Load())

	data, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	assert.Equal(t, other, string(data))
}

// TestStalePidFile pins that a pid file naming a dead process is replaced.
func TestStalePidFile(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "supervisor.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(deadPid(t))), 0o600))

	sup := newHarness(t, fixedSource(helperSpec(t, dir, modeServe), false), Options{
		PidFile:     pidFile,
		StopSignals: stopSignals,
	})
	sup.start()

	started := waitEvent[Started](t, sup.rec, 1)
	assert.Equal(t, os.Getpid(), readPid(t, pidFile))

	waitReady(t, dir, started.Pid)
	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())
	assert.NoFileExists(t, pidFile)
}

// TestChildPidFileRemovalFails pins that a child pid file that cannot be
// removed ends Run with OpChildPidFile instead of passing unnoticed.
func TestChildPidFileRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes files from a read-only directory")
	}

	dir := t.TempDir()
	runDir := filepath.Join(dir, "run")
	childPidFile := filepath.Join(runDir, "child.pid")

	sup := newHarness(t, fixedSource(helperSpec(t, dir, modeServe), true), Options{
		PidFile:      filepath.Join(dir, "supervisor.pid"),
		ChildPidFile: childPidFile,
		StopSignals:  stopSignals,
	})
	sup.start()

	started := waitEvent[Started](t, sup.rec, 1)
	waitReady(t, dir, started.Pid)

	require.NoError(t, os.Chmod(runDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(runDir, 0o700) })

	sup.send(syscall.SIGTERM)

	err := sup.wait()
	require.Error(t, err)
	assert.Equal(t, OpChildPidFile, errorOp(t, err))
	require.ErrorIs(t, err, fs.ErrPermission)
	assert.FileExists(t, childPidFile)
}
