package running

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/pidfile"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// newStateInstance is an instance whose run directory is a temporary one.
func newStateInstance(t *testing.T) *InstanceCtx {
	t.Helper()

	dir := t.TempDir()

	return &InstanceCtx{
		AppName: "myapp", InstName: "inst", RunDir: dir,
		PIDFile: filepath.Join(dir, "tt.pid"),
	}
}

// readStateFile decodes the state file of inst as it is.
func readStateFile(t *testing.T, inst *InstanceCtx) WatchdogState {
	t.Helper()

	data, err := os.ReadFile(watchdogStateFile(inst))
	require.NoError(t, err)

	var state WatchdogState

	require.NoError(t, json.Unmarshal(data, &state))

	return state
}

// assertOnlyStateFile asserts that the run directory of inst holds the state
// file alone: no file a write left behind.
func assertOnlyStateFile(t *testing.T, inst *InstanceCtx) {
	t.Helper()

	entries, err := os.ReadDir(inst.RunDir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	assert.Equal(t, []string{watchdogStateFileName}, names)
}

// TestStatePublisherPublishes pins what the watchdog publishes and when:
// itself once it holds the pid file, with no process yet, the process at
// every start, with the count of restarts, none after every exit, every
// integrity check, and nothing once it has given the pid file up.
func TestStatePublisherPublishes(t *testing.T) {
	inst := newStateInstance(t)
	clock := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	pub := newStatePublisher(inst)

	pub.now = func() time.Time { return clock }

	// The pid file of the child is not the pid file of the instance.
	require.NoError(t, pub.onEvent(supervisor.PidFileWritten{
		Path: filepath.Join(inst.RunDir, "process.pid"), Pid: 7,
	}))
	assert.NoFileExists(t, watchdogStateFile(inst))

	require.NoError(t, pub.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 7}))

	state := readStateFile(t, inst)
	assert.Equal(t, watchdogStateVersion, state.Version)
	assert.Equal(t, 7, state.Watchdog.PID)
	assert.False(t, state.Watchdog.StartedAt.IsZero())
	assert.Nil(t, state.Process, "no process runs before the first start")
	assert.Zero(t, state.Restarts)
	assert.Nil(t, state.IntegrityCheck)
	assertOnlyStateFile(t, inst)

	require.NoError(t, pub.onEvent(supervisor.Started{Pid: 10}))

	state = readStateFile(t, inst)
	require.NotNil(t, state.Process)
	assert.Equal(t, 10, state.Process.PID)
	assert.True(t, clock.Equal(state.Process.StartedAt))
	assert.Zero(t, state.Restarts, "the first start is no restart")

	require.NoError(t, pub.onEvent(exitWith(t, 1)))

	state = readStateFile(t, inst)
	assert.Nil(t, state.Process, "no process runs during the restart delay")
	assert.Zero(t, state.Restarts)

	// The events that change nothing of the state publish nothing.
	require.NoError(t, os.Remove(watchdogStateFile(inst)))
	require.NoError(t, pub.onEvent(supervisor.Restarting{Delay: time.Second}))
	assert.NoFileExists(t, watchdogStateFile(inst))

	clock = clock.Add(time.Minute)

	require.NoError(t, pub.onEvent(supervisor.Started{Pid: 11}))

	state = readStateFile(t, inst)
	require.NotNil(t, state.Process)
	assert.Equal(t, 11, state.Process.PID)
	assert.True(t, clock.Equal(state.Process.StartedAt))
	assert.Equal(t, 1, state.Restarts)
	assert.Equal(t, 7, state.Watchdog.PID)

	require.NoError(t, pub.onEvent(supervisor.Checked{Err: nil}))

	state = readStateFile(t, inst)
	require.NotNil(t, state.IntegrityCheck)
	assert.True(t, state.IntegrityCheck.OK)
	assert.Empty(t, state.IntegrityCheck.Error)
	assert.Equal(t, "passed, at 2026-11-23 10:01:00", state.IntegrityCheck.Describe())

	require.NoError(t, pub.onEvent(supervisor.Checked{Err: errors.New("app.lua changed")}))

	state = readStateFile(t, inst)
	require.NotNil(t, state.IntegrityCheck)
	assert.False(t, state.IntegrityCheck.OK)
	assert.Equal(t, "app.lua changed", state.IntegrityCheck.Error)
	assert.Equal(t, "failed, at 2026-11-23 10:01:00: app.lua changed",
		state.IntegrityCheck.Describe())
	require.NotNil(t, state.Process, "the check alone does not end the process")
	assertOnlyStateFile(t, inst)

	pub.unpublish()
	assert.NoFileExists(t, watchdogStateFile(inst))

	// Without the pid file the watchdog publishes nothing more.
	require.NoError(t, pub.onEvent(supervisor.Started{Pid: 12}))
	assert.NoFileExists(t, watchdogStateFile(inst))
}

// TestStatePublisherWithoutThePidFile pins that a watchdog that does not hold
// the pid file of the instance neither publishes its state nor removes the
// state the watchdog that holds it publishes: the instance runs under that
// one.
func TestStatePublisherWithoutThePidFile(t *testing.T) {
	inst := newStateInstance(t)
	other := []byte(`{"version":1,"watchdog":{"pid":3}}`)

	require.NoError(t, os.WriteFile(watchdogStateFile(inst), other, 0o600))

	pub := newStatePublisher(inst)

	require.NoError(t, pub.onEvent(supervisor.Started{Pid: 10}))
	require.NoError(t, pub.onEvent(supervisor.Checked{Err: nil}))
	pub.unpublish()

	data, err := os.ReadFile(watchdogStateFile(inst))
	require.NoError(t, err)
	assert.Equal(t, other, data)

	// An instance with no run directory has no state file.
	none := newStatePublisher(&InstanceCtx{PIDFile: inst.PIDFile})
	require.NoError(t, none.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 7}))
	none.unpublish()
}

// TestStatePublisherWritesWhole pins that a state is written whole, through a
// new file renamed into place: a reader that opened the old state reads it
// all, the path names the new one, and no other file is left behind. A state
// that cannot be written is an error that says so.
func TestStatePublisherWritesWhole(t *testing.T) {
	inst := newStateInstance(t)
	pub := newStatePublisher(inst)

	require.NoError(t, pub.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 7}))

	old, err := os.Open(watchdogStateFile(inst))
	require.NoError(t, err)

	defer func() { _ = old.Close() }()

	require.NoError(t, pub.onEvent(supervisor.Started{Pid: 10}))

	var before WatchdogState

	require.NoError(t, json.NewDecoder(old).Decode(&before))
	assert.Nil(t, before.Process, "the old state changed in place")

	after := readStateFile(t, inst)
	require.NotNil(t, after.Process)
	assert.Equal(t, 10, after.Process.PID)
	assertOnlyStateFile(t, inst)

	info, err := os.Stat(watchdogStateFile(inst))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(launchFileMode), info.Mode().Perm())

	require.NoError(t, os.RemoveAll(inst.RunDir))

	err = pub.onEvent(exitWith(t, 0))
	require.ErrorContains(t, err, "publishing the state of the watchdog")
}

// TestStatePublisherConcurrent pins that the state stays whole when its
// changes come from several goroutines at once, the periodic check running
// on one of its own: run with -race, it finds the writes serialized.
func TestStatePublisherConcurrent(t *testing.T) {
	const starts = 20

	inst := newStateInstance(t)
	pub := newStatePublisher(inst)

	require.NoError(t, pub.onEvent(supervisor.PidFileWritten{Path: inst.PIDFile, Pid: 7}))

	var writers sync.WaitGroup

	for pid := range starts {
		writers.Go(func() {
			assert.NoError(t, pub.onEvent(supervisor.Started{Pid: 100 + pid}))
		})
		writers.Go(func() {
			assert.NoError(t, pub.onEvent(supervisor.Checked{Err: nil}))
		})
	}

	writers.Wait()

	state := readStateFile(t, inst)
	assert.Equal(t, starts-1, state.Restarts)
	require.NotNil(t, state.IntegrityCheck)
	assert.True(t, state.IntegrityCheck.OK)
	assertOnlyStateFile(t, inst)
}

// TestReadWatchdogState pins that the state of the watchdog counts only when
// it names the watchdog that holds the pid file: a missing one, or one a
// watchdog that died left, is none, and one of another version of the format
// is an error of its own.
func TestReadWatchdogState(t *testing.T) {
	inst := newStateInstance(t)
	write := func(state string) {
		require.NoError(t, os.WriteFile(watchdogStateFile(inst), []byte(state), 0o600))
	}

	_, err := ReadWatchdogState(inst, 7)
	require.ErrorIs(t, err, ErrNoWatchdogState)

	write(`{"version":1,"watchdog":{"pid":7,"started_at":"2026-11-23T10:00:00Z"},` +
		`"process":{"pid":10,"started_at":"2026-11-23T10:00:01Z"},"restarts":2}`)

	state, err := ReadWatchdogState(inst, 7)
	require.NoError(t, err)
	require.NotNil(t, state.Process)
	assert.Equal(t, 10, state.Process.PID)
	assert.Equal(t, 2, state.Restarts)
	assert.Nil(t, state.IntegrityCheck)

	// The state of a watchdog that is gone.
	_, err = ReadWatchdogState(inst, 8)
	require.ErrorIs(t, err, ErrNoWatchdogState)

	_, err = ReadWatchdogState(inst, 0)
	require.ErrorIs(t, err, ErrNoWatchdogState)

	// A state of another version is not read any further.
	write(`{"version":2,"watchdog":"7"}`)

	_, err = ReadWatchdogState(inst, 7)
	require.ErrorIs(t, err, ErrWatchdogStateVersion)
	require.ErrorContains(t, err, "version 2, not 1")

	write(`{`)

	_, err = ReadWatchdogState(inst, 7)
	require.ErrorContains(t, err, "decoding the state of the watchdog")
	require.NotErrorIs(t, err, ErrNoWatchdogState)

	_, err = ReadWatchdogState(&InstanceCtx{}, 7)
	require.ErrorIs(t, err, ErrNoWatchdogState)
}

// TestInstancePIDs pins how the messages of the commands name an instance:
// by its process, as its watchdog publishes it; by the watchdog, labelled
// so, when the process is not known; by both where the user may have to act
// by hand, as after a stop that failed; and by neither for a watchdog that
// has not written its pid yet.
func TestInstancePIDs(t *testing.T) {
	inst := newStateInstance(t)

	assert.Equal(t, " (watchdog PID = 7)", pidsOf(inst, 7).String())
	assert.Equal(t, " (watchdog PID = 7)", pidsOf(inst, 7).withWatchdog())
	assert.Equal(t, " (watchdog PID = 7)", DescribePID(inst, 7))

	publishTestState(t, inst, 7, 10)

	assert.Equal(t, instancePIDs{process: 10, watchdog: 7}, pidsOf(inst, 7))
	assert.Equal(t, " (PID = 10)", pidsOf(inst, 7).String())
	assert.Equal(t, " (PID = 10, watchdog PID = 7)", pidsOf(inst, 7).withWatchdog())
	assert.Equal(t, " (PID = 10)", DescribePID(inst, 7))

	// The state of another watchdog, one that died, says nothing of this
	// one.
	assert.Equal(t, " (watchdog PID = 8)", pidsOf(inst, 8).withWatchdog())

	assert.Empty(t, pidsOf(inst, 0).String())
	assert.Empty(t, pidsOf(inst, 0).withWatchdog())
	assert.Empty(t, DescribePID(inst, 0))
}

// TestStaleStateGoesWithTheLeftovers pins that the state a watchdog that
// died left goes with its other leftovers: when a stale pid file is removed,
// and after tt instance kill.
func TestStaleStateGoesWithTheLeftovers(t *testing.T) {
	gone := 1<<31 - 2

	t.Run("stale pid file", func(t *testing.T) {
		inst := newStateInstance(t)

		require.NoError(t, os.WriteFile(inst.PIDFile, []byte(strconv.Itoa(gone)), 0o600))
		require.NoError(t, os.WriteFile(watchdogStateFile(inst), []byte(`{}`), 0o600))

		removed, err := removeStale(inst, gone)
		require.NoError(t, err)
		assert.True(t, removed)
		assert.NoFileExists(t, watchdogStateFile(inst))
	})

	t.Run("a live watchdog keeps it", func(t *testing.T) {
		inst := newStateInstance(t)

		owned, err := pidfile.Acquire(inst.PIDFile, os.Getpid())
		require.NoError(t, err)

		defer func() { require.NoError(t, owned.Release()) }()

		require.NoError(t, os.WriteFile(watchdogStateFile(inst), []byte(`{}`), 0o600))

		_, err = removeStale(inst, os.Getpid())
		require.Error(t, err)
		assert.FileExists(t, watchdogStateFile(inst))
	})

	t.Run("kill", func(t *testing.T) {
		run := killRun(t)

		run.RunDir = filepath.Dir(run.PIDFile)
		require.NoError(t, os.WriteFile(watchdogStateFile(run), []byte(`{}`), 0o600))

		watchdog := fakeWatchdog(t, run)

		require.NoError(t, Kill(*run))

		_, err := watchdog.Process.Wait()
		require.NoError(t, err)
		assert.NoFileExists(t, watchdogStateFile(run))
	})
}
