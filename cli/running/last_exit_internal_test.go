package running

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

func newLastExitInstance(t *testing.T) *InstanceCtx {
	t.Helper()

	dir := t.TempDir()

	return &InstanceCtx{
		AppName: "myapp", InstName: "inst", RunDir: dir,
		PIDFile: filepath.Join(dir, "tt.pid"),
	}
}

func TestExitRecorderRecordsAnExit(t *testing.T) {
	inst := newLastExitInstance(t)
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	rec := newExitRecorder(inst)

	rec.now = func() time.Time { return at }

	rec.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 1})
	rec.onEvent(supervisor.Started{Pid: 2})
	rec.onEvent(exitWith(t, 3))

	last, ok := ReadLastExit(inst)
	require.True(t, ok)
	assert.Equal(t, "it exited with code 3", last.Exited)
	assert.False(t, last.Stopped)
	assert.False(t, last.Clean())
	assert.Equal(t, "it exited with code 3, at 2026-11-17 10:00:00", last.Describe())

	// The watchdog ends with a failure: it goes into the record of the exit.
	rec.end(errors.New("boom"))

	last, ok = ReadLastExit(inst)
	require.True(t, ok)
	assert.Equal(t, "it exited with code 3; its watchdog ended: boom, at 2026-11-17 10:00:00",
		last.Describe())
}

func TestExitRecorderRecordsAStop(t *testing.T) {
	inst := newLastExitInstance(t)

	rec := newExitRecorder(inst)
	rec.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 1})
	rec.onEvent(supervisor.Started{Pid: 2})
	rec.onEvent(supervisor.SignalReceived{Signal: syscall.SIGTERM, Action: supervisor.ActionStop})
	rec.onEvent(exitWith(t, 0))
	rec.end(nil)

	last, ok := ReadLastExit(inst)
	require.True(t, ok)
	assert.True(t, last.Stopped)
	assert.True(t, last.Clean())
	assert.Contains(t, last.Describe(), "stopped, it exited with code 0, at ")
}

func TestExitRecorderForgetsTheLastLife(t *testing.T) {
	inst := newLastExitInstance(t)

	rec := newExitRecorder(inst)
	rec.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 1})
	rec.onEvent(exitWith(t, 1))

	_, ok := ReadLastExit(inst)
	require.True(t, ok)

	// The next watchdog that holds the pid file removes the record: the
	// instance runs again.
	next := newExitRecorder(inst)
	next.onEvent(supervisor.PidFileWritten{Path: filepath.Join(inst.RunDir, "other"), Pid: 2})

	_, ok = ReadLastExit(inst)
	require.True(t, ok, "the pid file of the child is no new life")

	next.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 3})

	_, ok = ReadLastExit(inst)
	assert.False(t, ok)
}

func TestExitRecorderLeavesARunningInstance(t *testing.T) {
	inst := newLastExitInstance(t)

	// A watchdog that could not take the pid file says nothing of the
	// instance, which may run under another one.
	rec := newExitRecorder(inst)
	rec.end(&supervisor.Error{Op: supervisor.OpPidFile, Err: errors.New("locked")})

	_, ok := ReadLastExit(inst)
	assert.False(t, ok)

	// One that failed before it started anything says why.
	rec.end(errors.New("failed to create a directory: permission denied"))

	last, ok := ReadLastExit(inst)
	require.True(t, ok)
	assert.Empty(t, last.Exited)
	assert.Contains(t, last.Describe(),
		"its watchdog ended: failed to create a directory: permission denied, at ")
}

func TestRecordKilled(t *testing.T) {
	inst := newLastExitInstance(t)

	recordKilled(inst)

	last, ok := ReadLastExit(inst)
	require.True(t, ok)
	assert.Equal(t, "it was killed by tt instance kill", last.Exited)
	assert.False(t, last.Clean())
}

func TestReadLastExitOfNone(t *testing.T) {
	inst := newLastExitInstance(t)

	_, ok := ReadLastExit(inst)
	assert.False(t, ok)

	require.NoError(t, os.WriteFile(lastExitFile(inst), []byte("{"), 0o644))

	_, ok = ReadLastExit(inst)
	assert.False(t, ok)

	_, ok = ReadLastExit(&InstanceCtx{})
	assert.False(t, ok)
}
