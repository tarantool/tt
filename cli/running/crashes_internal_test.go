package running

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// Wait statuses as Linux and macOS encode them: the signal in the low seven
// bits, 0x80 for a core dumped, the exit code in the next byte.
const (
	segvWithCore    = syscall.WaitStatus(0x80 | 11)
	killedWithout   = syscall.WaitStatus(9)
	exitedWithCode1 = syscall.WaitStatus(1 << 8)
)

// crashedAt is when the crashes of the tests were.
var crashedAt = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

// TestDescribeSignal pins how an exit by a signal is told, with the core the
// kernel dumped of it as the shell tells it.
func TestDescribeSignal(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "it was killed by signal 11 (segmentation fault), core dumped",
		describeSignal(syscall.SIGSEGV, true))
	assert.Equal(t, "it was killed by signal 9 (killed)", describeSignal(syscall.SIGKILL, false))
}

// TestCrashOfStatus pins which exits are crashes: those by a signal, with
// or without a core.
func TestCrashOfStatus(t *testing.T) {
	t.Parallel()

	crash, ok := crashOfStatus(42, segvWithCore, crashedAt)
	require.True(t, ok)
	assert.Equal(t, Crash{At: crashedAt, PID: 42, Signal: syscall.SIGSEGV, CoreDumped: true}, crash)
	assert.Equal(t, "PID 42, it was killed by signal 11 (segmentation fault), core dumped, "+
		"at 2026-11-24 10:00:00", crash.Describe())

	crash, ok = crashOfStatus(43, killedWithout, crashedAt)
	require.True(t, ok)
	assert.Equal(t, Crash{At: crashedAt, PID: 43, Signal: syscall.SIGKILL, CoreDumped: false},
		crash)

	_, ok = crashOfStatus(44, exitedWithCode1, crashedAt)
	assert.False(t, ok, "an exit with a code is no crash")

	_, ok = crashOf(supervisor.Exit{Pid: 45, State: nil, Err: os.ErrProcessDone}, crashedAt)
	assert.False(t, ok, "an exit that could not be waited for is no crash")

	_, ok = crashOf(exitWith(t, 3), crashedAt)
	assert.False(t, ok)
}

// TestCrashOfKilledProcess checks the crash of a process a signal really
// killed: the wait status of the process, not one made up.
func TestCrashOfKilledProcess(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, cmd.Start())
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))

	err := cmd.Wait()

	crash, ok := crashOf(supervisor.Exit{Pid: cmd.Process.Pid, State: cmd.ProcessState, Err: err},
		time.Now())
	require.True(t, ok)
	assert.Equal(t, cmd.Process.Pid, crash.PID)
	assert.Equal(t, syscall.SIGTERM, crash.Signal)
	assert.False(t, crash.CoreDumped)
}

// readCrashes returns the crashes of inst, which are to be read.
func readCrashes(t *testing.T, inst *InstanceCtx) []Crash {
	t.Helper()

	all, err := ReadCrashes(inst)
	require.NoError(t, err)

	return all
}

// TestRecordCrashKeepsTheLast pins the rotation of the crashes: the last
// crashesLimit of them, the oldest first, in a file of the version.
func TestRecordCrashKeepsTheLast(t *testing.T) {
	t.Parallel()

	inst := newLastExitInstance(t)

	for i := range crashesLimit + 3 {
		assert.Empty(t, recordCrash(inst, Crash{
			At: crashedAt.Add(time.Duration(i) * time.Minute), PID: 100 + i,
			Signal: syscall.SIGSEGV, CoreDumped: i%2 == 0,
		}, crashedAt))
	}

	all := readCrashes(t, inst)
	require.Len(t, all, crashesLimit)
	assert.Equal(t, 103, all[0].PID, "the oldest three are dropped")
	assert.Equal(t, 100+crashesLimit+2, all[len(all)-1].PID)

	last, ok, err := LastCrash(inst)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, all[len(all)-1], last)

	data, err := os.ReadFile(crashesFile(inst))
	require.NoError(t, err)

	var raw map[string]any

	require.NoError(t, json.Unmarshal(data, &raw))
	assert.InDelta(t, crashesVersion, raw["version"], 0)

	first := raw["crashes"].([]any)[0].(map[string]any) //nolint:forcetypeassert // Pinned above.
	assert.Equal(t, map[string]any{
		"at": "2026-11-24T10:03:00Z", "pid": float64(103), "signal": float64(11),
		"core_dumped": false,
	}, first)
}

// TestRecordCrashWritesWhole checks that the crashes are written through a
// file renamed into place: nothing else is left in the run directory but a
// record that cannot be read, which is moved aside, by the time of the
// crash, with a warning, and the crashes are recorded anew. A run directory
// that cannot be written to leaves the record as it is, and says so.
func TestRecordCrashWritesWhole(t *testing.T) {
	t.Parallel()

	inst := newLastExitInstance(t)
	broken := []byte("{\"version\": 1, \"crashes\": [")

	require.NoError(t, os.WriteFile(crashesFile(inst), broken, 0o600))

	_, err := ReadCrashes(inst)
	require.ErrorIs(t, err, errCrashesMalformed)

	crash := Crash{At: time.Now().UTC(), PID: 7, Signal: syscall.SIGABRT, CoreDumped: true}
	aside := crashesFileName + ".20261124T100000.000000000Z"

	assert.Equal(t, []string{"cannot read the crashes of " + crashesFile(inst) + ": not a " +
		"record of crashes: unexpected end of JSON input; it is moved to " +
		filepath.Join(inst.RunDir, aside) + ", and the crashes are recorded anew"},
		recordCrash(inst, crash, crashedAt))

	entries, err := os.ReadDir(inst.RunDir)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, crashesFileName, entries[0].Name())
	assert.Equal(t, aside, entries[1].Name())

	kept, err := os.ReadFile(filepath.Join(inst.RunDir, aside))
	require.NoError(t, err)
	assert.Equal(t, broken, kept)

	all := readCrashes(t, inst)
	require.Len(t, all, 1)
	assert.Equal(t, crash.PID, all[0].PID)
	assert.True(t, crash.At.Equal(all[0].At))

	// A run directory that cannot be written to leaves the record as it is.
	require.NoError(t, os.Chmod(inst.RunDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(inst.RunDir, 0o700) })

	if os.Geteuid() != 0 {
		warnings := recordCrash(inst, Crash{
			At: time.Now(), PID: 8, Signal: syscall.SIGSEGV, CoreDumped: true,
		}, crashedAt)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "cannot record the crash of PID 8: cannot write the "+
			"crashes: ")
		assert.Contains(t, warnings[0], "permission denied")
		assert.Len(t, readCrashes(t, inst), 1)
	}
}

// TestRecordCrashOfNewerTT checks that a downgraded tt keeps the record of a
// newer one: reading it says so, and the watchdog moves it aside, where the
// newer tt finds it, and records the crash anew; a record it cannot move
// aside is kept, and the crash is not recorded, with the warning of why.
func TestRecordCrashOfNewerTT(t *testing.T) {
	t.Parallel()

	inst := newLastExitInstance(t)
	newer := []byte(`{"version": 2, "crashes": [{"pid": 1, "signal": 11}]}`)

	require.NoError(t, os.WriteFile(crashesFile(inst), newer, 0o600))

	_, err := ReadCrashes(inst)
	require.ErrorIs(t, err, ErrCrashesNewer)
	require.EqualError(t, err, "cannot read the crashes of "+crashesFile(inst)+": written by a "+
		"newer tt, of version 2 of the format, and this one reads 1")

	_, ok, err := LastCrash(inst)
	require.ErrorIs(t, err, ErrCrashesNewer)
	assert.False(t, ok)

	crash := Crash{At: crashedAt, PID: 7, Signal: syscall.SIGABRT, CoreDumped: true}
	aside := filepath.Join(inst.RunDir, crashesFileName+".20261124T100000.000000000Z")

	warnings := recordCrash(inst, crash, crashedAt)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "written by a newer tt, of version 2 of the format, and this "+
		"one reads 1; it is moved to "+aside+", and the crashes are recorded anew")

	kept, err := os.ReadFile(aside)
	require.NoError(t, err)
	assert.Equal(t, newer, kept)
	assert.Equal(t, []Crash{crash}, readCrashes(t, inst))

	// A record of a newer tt in a run directory that cannot be written to.
	if os.Geteuid() == 0 {
		return
	}

	stuck := newLastExitInstance(t)

	require.NoError(t, os.WriteFile(crashesFile(stuck), newer, 0o600))
	require.NoError(t, os.Chmod(stuck.RunDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(stuck.RunDir, 0o700) })

	warnings = recordCrash(stuck, crash, crashedAt)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "written by a newer tt, of version 2 of the format, and this "+
		"one reads 1; it cannot be moved aside either, and the crash of PID 7 is not recorded: ")
	assert.Contains(t, warnings[0], "permission denied")

	kept, err = os.ReadFile(crashesFile(stuck))
	require.NoError(t, err)
	assert.Equal(t, newer, kept)
}

// TestReadCrashesOfNone pins the instances that have no crashes: no run
// directory and no file. A file that cannot be read is an error that says
// why: one tt may not read, as one of another user, which the watchdog
// writes for its own alone, one of a version of the format before the
// first, and one that is no record.
func TestReadCrashesOfNone(t *testing.T) {
	t.Parallel()

	assert.Empty(t, readCrashes(t, &InstanceCtx{}))

	_, ok, err := LastCrash(&InstanceCtx{})
	require.NoError(t, err)
	assert.False(t, ok)

	inst := newLastExitInstance(t)
	assert.Empty(t, readCrashes(t, inst))

	require.NoError(t, os.WriteFile(crashesFile(inst), []byte(`{"crashes": []}`), 0o600))

	_, err = ReadCrashes(inst)
	require.ErrorIs(t, err, errCrashesMalformed)
	require.ErrorContains(t, err, "version 0 of the format")

	if os.Geteuid() == 0 {
		return
	}

	require.NoError(t, os.Chmod(crashesFile(inst), 0o000))

	_, err = ReadCrashes(inst)
	require.ErrorIs(t, err, os.ErrPermission)
	require.EqualError(t, err, "cannot read the crashes: open "+crashesFile(inst)+
		": permission denied")
}

// TestExitRecorderKeepsTheCrashes checks that the watchdog records a crash
// with its PID and its core in the last exit and in the crashes, and that
// the crashes outlive the next life of the instance, which removes the last
// exit, and its clean stop: a crash that a restart followed is not lost.
func TestExitRecorderKeepsTheCrashes(t *testing.T) {
	t.Parallel()

	inst := newLastExitInstance(t)

	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, cmd.Start())
	require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))

	waitErr := cmd.Wait()

	rec := newExitRecorder(inst)

	rec.now = func() time.Time { return crashedAt }

	rec.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 1})
	rec.onEvent(supervisor.Started{Pid: cmd.Process.Pid})
	rec.onEvent(supervisor.Exit{Pid: cmd.Process.Pid, State: cmd.ProcessState, Err: waitErr})

	last, ok := ReadLastExit(inst)
	require.True(t, ok)
	assert.Equal(t, cmd.Process.Pid, last.PID)
	assert.False(t, last.CoreDumped)
	assert.Equal(t, "it was killed by signal 9 (killed)", last.Exited)

	want := []Crash{
		{At: crashedAt, PID: cmd.Process.Pid, Signal: syscall.SIGKILL, CoreDumped: false},
	}
	assert.Equal(t, want, readCrashes(t, inst))

	// The restart policy starts it again, and a stop ends it cleanly.
	rec.onEvent(supervisor.Started{Pid: 3})
	rec.onEvent(supervisor.SignalReceived{Signal: syscall.SIGINT, Action: supervisor.ActionStop})
	rec.onEvent(exitWith(t, 0))
	rec.end(nil)

	last, ok = ReadLastExit(inst)
	require.True(t, ok)
	assert.True(t, last.Clean())
	assert.Zero(t, last.CoreDumped)
	assert.Equal(t, want, readCrashes(t, inst), "a clean exit is no crash")

	// The next life of the instance, as systemd starts it again, removes
	// the last exit and keeps the crashes.
	next := newExitRecorder(inst)
	next.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 4})

	_, ok = ReadLastExit(inst)
	assert.False(t, ok)
	assert.Equal(t, want, readCrashes(t, inst))

	// A record a newer tt wrote is moved aside, and the log warns of it.
	require.NoError(t, os.WriteFile(crashesFile(inst), []byte(`{"version": 9}`), 0o600))

	var warned []string

	next.now = func() time.Time { return crashedAt }
	next.warn = func(message string) { warned = append(warned, message) }
	next.onEvent(supervisor.Started{Pid: cmd.Process.Pid})
	next.onEvent(supervisor.Exit{Pid: cmd.Process.Pid, State: cmd.ProcessState, Err: waitErr})

	require.Len(t, warned, 1)
	assert.Contains(t, warned[0], "written by a newer tt, of version 9 of the format")
	assert.Equal(t, want, readCrashes(t, inst))
	assert.FileExists(t, crashesFile(inst)+".20261124T100000.000000000Z")
}

// TestLastExitWithPID pins the fields the record of the last exit gained:
// the PID and the core, left out when they are not known, so that a record
// of a tt before them reads the same.
func TestLastExitWithPID(t *testing.T) {
	t.Parallel()

	inst := newLastExitInstance(t)

	writeLastExit(inst, &LastExit{
		At: crashedAt, Exited: describeSignal(syscall.SIGSEGV, true), PID: 42, CoreDumped: true,
	})

	data, err := os.ReadFile(filepath.Join(inst.RunDir, lastExitFileName))
	require.NoError(t, err)
	assert.JSONEq(t, `{"at": "2026-11-24T10:00:00Z", "pid": 42, "core_dumped": true,
		"exited": "it was killed by signal 11 (segmentation fault), core dumped"}`, string(data))

	recordKilled(inst)

	data, err = os.ReadFile(filepath.Join(inst.RunDir, lastExitFileName))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "pid")
	assert.NotContains(t, string(data), "core_dumped")
}
