package pidfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pidIn reads a pid file, checking its exact format.
func pidIn(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err, "the pid file holds %q", data)

	return pid
}

// deadPid returns the pid of a process that has exited and been waited for.
func deadPid(t *testing.T) int {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "true")
	require.NoError(t, cmd.Run())

	return cmd.Process.Pid
}

// sleeper starts a process that runs until the test ends.
func sleeper(t *testing.T) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	return cmd
}

// acquired is the outcome of one attempt to own a pid file.
type acquired struct {
	owned *File
	err   error
}

// TestStaleTakeover has many acquirers race for one stale pid file, round
// after round: exactly one of them may own it. They do not retry a busy
// lock, which would only have every loser wait busyWait for a winner that
// keeps the file until the round is over.
func TestStaleTakeover(t *testing.T) {
	const (
		rounds    = 300
		acquirers = 8
	)

	dir := t.TempDir()
	stale := []byte(strconv.Itoa(deadPid(t)))

	for round := range rounds {
		path := filepath.Join(dir, "round"+strconv.Itoa(round)+".pid")
		require.NoError(t, os.WriteFile(path, stale, 0o600))

		var (
			start   sync.WaitGroup
			racers  sync.WaitGroup
			results = make(chan acquired, acquirers)
		)

		start.Add(1)

		for range acquirers {
			racers.Go(func() {
				start.Wait()

				owned, err := acquire(path, os.Getpid(), 0)
				results <- acquired{owned: owned, err: err}
			})
		}

		start.Done()
		racers.Wait()
		close(results)

		var winners []*File

		for result := range results {
			if result.err != nil {
				require.ErrorIs(t, result.err, ErrBusy)

				continue
			}

			winners = append(winners, result.owned)
		}

		require.Len(t, winners, 1, "round %d", round)
		assert.Equal(t, os.Getpid(), pidIn(t, path))
		require.NoError(t, winners[0].Release())
		assert.NoFileExists(t, path)
	}
}

// TestExclusive has acquirers take, hold and release one pid file in a loop:
// however acquiring and releasing interleave, no two of them may own it at
// once. They do not retry a busy lock: one that waits for the file sees it
// replaced by every owner that comes and goes meanwhile, until it gives up
// on a file that keeps being replaced.
func TestExclusive(t *testing.T) {
	const (
		acquirers = 8
		attempts  = 400
	)

	path := filepath.Join(t.TempDir(), "x.pid")

	var (
		racers     sync.WaitGroup
		owners     atomic.Int32
		overlap    atomic.Int32
		orphaned   atomic.Int32
		owned      atomic.Int32
		unexpected = make(chan error, acquirers*attempts)
	)

	for range acquirers {
		racers.Go(func() {
			for range attempts {
				file, err := acquire(path, os.Getpid(), 0)
				if err != nil {
					if !errors.Is(err, ErrBusy) {
						unexpected <- err
					}

					continue
				}

				owned.Add(1)

				if owners.Add(1) != 1 {
					overlap.Add(1)
				}

				// While it is owned, nobody can replace it: the owner's file
				// is the one tt instance stop and tt instance status read.
				same, err := stillAt(file.file, path)
				if err != nil || !same {
					orphaned.Add(1)
				}

				time.Sleep(20 * time.Microsecond)
				owners.Add(-1)

				err = file.Release()
				if err != nil {
					unexpected <- err
				}
			}
		})
	}

	racers.Wait()
	close(unexpected)

	for err := range unexpected {
		require.NoError(t, err)
	}

	assert.Zero(t, overlap.Load(), "two owners of one pid file at once")
	assert.Zero(t, orphaned.Load(), "an owner of a file no longer at the path")
	assert.Positive(t, owned.Load())
	t.Logf("%d of %d attempts owned the pid file", owned.Load(), acquirers*attempts)
}

// TestLockNotInherited pins that a child does not inherit the lock: once the
// owner is gone, the file can be taken over while the child still runs.
func TestLockNotInherited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.pid")

	owned, err := Acquire(path, os.Getpid())
	require.NoError(t, err)

	sleeper(t)

	// The owner dies without removing the file.
	require.NoError(t, owned.file.Close())

	again, err := Acquire(path, os.Getpid())
	require.NoError(t, err, "the child holds the lock")
	require.NoError(t, again.Release())
}

// TestLocklessOwner pins that a pid file written without a lock is still
// refused while the process it names runs, and taken over once that process
// is gone.
func TestLocklessOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.pid")
	cmd := sleeper(t)
	other := strconv.Itoa(cmd.Process.Pid)

	require.NoError(t, os.WriteFile(path, []byte(other), 0o600))

	_, err := Acquire(path, os.Getpid())
	require.ErrorIs(t, err, ErrBusy)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, other, string(data))

	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait())

	owned, err := Acquire(path, os.Getpid())
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), pidIn(t, path))
	require.NoError(t, owned.Release())
}

// TestRelease pins that releasing removes only the file this owner locked,
// not one that replaced it.
func TestRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.pid")

	owned, err := Acquire(path, os.Getpid())
	require.NoError(t, err)
	require.NoError(t, owned.Release())
	assert.NoFileExists(t, path)

	owned, err = Acquire(path, os.Getpid())
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.WriteFile(path, []byte("42"), 0o600))
	require.NoError(t, owned.Release())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "42", string(data), "another file at the path was removed")
}

// TestKeep pins what a writer that exits leaves behind: the file stays and
// names the process it wrote, which keeps it until that process is gone.
func TestKeep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.pid")
	cmd := sleeper(t)

	owned, err := Acquire(path, cmd.Process.Pid)
	require.NoError(t, err)
	require.NoError(t, owned.Keep())
	assert.Equal(t, cmd.Process.Pid, pidIn(t, path))

	_, err = Acquire(path, os.Getpid())
	require.ErrorIs(t, err, ErrBusy, "the process the file names still runs")

	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait())

	again, err := Acquire(path, os.Getpid())
	require.NoError(t, err)
	require.NoError(t, again.Release())
}

// TestRemoveFor pins the verified remove: it removes the file of a process
// that is gone, and leaves a missing file, a file that names another
// process, and a file that a live owner still holds.
func TestRemoveFor(t *testing.T) {
	dir := t.TempDir()
	gone := deadPid(t)

	t.Run("the file of the process", func(t *testing.T) {
		path := filepath.Join(dir, "gone.pid")
		require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(gone)), 0o600))

		removed, err := RemoveFor(path, gone, time.Second, nil)
		require.NoError(t, err)
		assert.True(t, removed)
		assert.NoFileExists(t, path)
	})

	t.Run("missing", func(t *testing.T) {
		removed, err := RemoveFor(filepath.Join(dir, "missing.pid"), gone, time.Second,
			func() { t.Error("cleanup ran without a file") })
		require.NoError(t, err)
		assert.False(t, removed)
	})

	t.Run("a newer owner", func(t *testing.T) {
		path := filepath.Join(dir, "newer.pid")

		owned, err := Acquire(path, os.Getpid())
		require.NoError(t, err)

		defer func() { require.NoError(t, owned.Release()) }()

		removed, err := RemoveFor(path, gone, 50*time.Millisecond,
			func() { t.Error("cleanup ran for a newer owner") })
		require.ErrorIs(t, err, ErrBusy, "the owner holds the lock")
		assert.False(t, removed)
		assert.Equal(t, os.Getpid(), pidIn(t, path))
	})

	t.Run("a newer owner that exited", func(t *testing.T) {
		path := filepath.Join(dir, "kept.pid")
		cmd := sleeper(t)

		owned, err := Acquire(path, cmd.Process.Pid)
		require.NoError(t, err)
		require.NoError(t, owned.Keep())

		removed, err := RemoveFor(path, gone, time.Second,
			func() { t.Error("cleanup ran for another process") })
		require.NoError(t, err)
		assert.False(t, removed, "the file names another process")
		assert.Equal(t, cmd.Process.Pid, pidIn(t, path))
	})

	t.Run("the lock is dropped when the owner exits", func(t *testing.T) {
		path := filepath.Join(dir, "killed.pid")
		pid := lockInChild(t, path)

		removed, err := RemoveFor(path, pid, 5*time.Second, nil)
		require.NoError(t, err)
		assert.True(t, removed)
		assert.NoFileExists(t, path)
	})

	t.Run("cleanup runs under the lock", func(t *testing.T) {
		path := filepath.Join(dir, "cleanup.pid")
		pid := lockInChild(t, path)
		cleanups := 0

		removed, err := RemoveFor(path, pid, 5*time.Second, func() {
			cleanups++

			// A process starting now finds the file still there and
			// locked, so it cannot own what the cleanup removes.
			assert.Equal(t, pid, pidIn(t, path))

			_, err := Acquire(path, os.Getpid())
			assert.ErrorIs(t, err, ErrBusy, "the file is not locked during the cleanup")
		})
		require.NoError(t, err)
		assert.True(t, removed)
		assert.Equal(t, 1, cleanups)
		assert.NoFileExists(t, path)

		owned, err := Acquire(path, os.Getpid())
		require.NoError(t, err, "the file is still locked after RemoveFor")
		require.NoError(t, owned.Release())
	})
}

// lockInChild has a child process own the pid file at path, then kills the
// child with SIGKILL, as tt instance kill does, and returns its pid.
func lockInChild(t *testing.T, path string) int {
	t.Helper()

	cmd, _ := holdInChild(t, path)
	require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))

	return cmd.Process.Pid
}

// holdInChild has a child process own the pid file at path and returns it,
// with a channel closed once it has exited and been reaped. The child runs
// until the test kills it or ends. It holds the lock through an inherited
// descriptor, since the test cannot run this package in a child of its own.
func holdInChild(t *testing.T, path string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), "sleep", "60")

	cmd.ExtraFiles = []*os.File{file}
	require.NoError(t, cmd.Start())

	done := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-done
	})

	// The lock belongs to the open file description, which the child
	// shares; once the test closes its descriptor, the child alone holds it.
	require.NoError(t, syscall.Flock(int(file.Fd()), syscall.LOCK_EX))

	_, err = file.WriteAt([]byte(strconv.Itoa(cmd.Process.Pid)), 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, err = Acquire(path, os.Getpid())
	require.ErrorIs(t, err, ErrBusy, "the child does not hold the lock")

	return cmd, done
}

// TestAlive pins how a pid file is judged without owning it: it names a live
// process while that process runs, and no process once it is gone, when the
// file is missing or when it holds no pid.
func TestAlive(t *testing.T) {
	dir := t.TempDir()

	pid, live, err := Alive(filepath.Join(dir, "missing.pid"))
	require.NoError(t, err)
	assert.False(t, live)
	assert.Zero(t, pid)

	garbage := filepath.Join(dir, "garbage.pid")
	require.NoError(t, os.WriteFile(garbage, []byte("not a pid"), 0o600))

	_, live, err = Alive(garbage)
	require.NoError(t, err)
	assert.False(t, live)

	path := filepath.Join(dir, "x.pid")
	cmd := sleeper(t)

	owned, err := Acquire(path, cmd.Process.Pid)
	require.NoError(t, err)
	require.NoError(t, owned.Keep())

	pid, live, err = Alive(path)
	require.NoError(t, err)
	assert.True(t, live)
	assert.Equal(t, cmd.Process.Pid, pid)

	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait())

	_, live, err = Alive(path)
	require.NoError(t, err)
	assert.False(t, live, "the process the file names is gone")
}

// TestOwner pins how a reader tells an owned pid file from a stale one: by
// the lock alone, whatever process the pid in the file names.
func TestOwner(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing", func(t *testing.T) {
		pid, owned, err := Owner(filepath.Join(dir, "missing.pid"))
		require.NoError(t, err)
		assert.False(t, owned)
		assert.Zero(t, pid)
	})

	t.Run("no pid", func(t *testing.T) {
		path := filepath.Join(dir, "garbage.pid")
		require.NoError(t, os.WriteFile(path, []byte("not a pid"), 0o600))

		pid, owned, err := Owner(path)
		require.NoError(t, err)
		assert.False(t, owned)
		assert.Zero(t, pid)
	})

	t.Run("owned by this process", func(t *testing.T) {
		path := filepath.Join(dir, "own.pid")

		file, err := Acquire(path, os.Getpid())
		require.NoError(t, err)

		pid, owned, err := Owner(path)
		require.NoError(t, err)
		assert.True(t, owned)
		assert.Equal(t, os.Getpid(), pid)

		require.NoError(t, file.Release())

		pid, owned, err = Owner(path)
		require.NoError(t, err)
		assert.False(t, owned)
		assert.Zero(t, pid, "a released file is gone")
	})

	t.Run("held through another descriptor", func(t *testing.T) {
		path := filepath.Join(dir, "descriptor.pid")
		require.NoError(t, os.WriteFile(path, []byte("42"), 0o600))

		file, err := os.Open(path)
		require.NoError(t, err)

		defer func() { require.NoError(t, file.Close()) }()

		require.NoError(t, syscall.Flock(int(file.Fd()), syscall.LOCK_EX))

		pid, owned, err := Owner(path)
		require.NoError(t, err)
		assert.True(t, owned)
		assert.Equal(t, 42, pid)
	})

	t.Run("held by another process until it dies", func(t *testing.T) {
		path := filepath.Join(dir, "child.pid")
		cmd, done := holdInChild(t, path)

		pid, owned, err := Owner(path)
		require.NoError(t, err)
		assert.True(t, owned)
		assert.Equal(t, cmd.Process.Pid, pid)

		require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))
		<-done

		pid, owned, err = Owner(path)
		require.NoError(t, err)
		assert.False(t, owned, "the kernel drops the lock of a dead owner")
		assert.Equal(t, cmd.Process.Pid, pid)
	})

	t.Run("stale, naming a process that is gone", func(t *testing.T) {
		path := filepath.Join(dir, "gone.pid")
		gone := deadPid(t)
		require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(gone)), 0o600))

		pid, owned, err := Owner(path)
		require.NoError(t, err)
		assert.False(t, owned)
		assert.Equal(t, gone, pid)
	})

	t.Run("stale, naming a live process", func(t *testing.T) {
		// The pid of a dead owner reused by an unrelated process.
		path := filepath.Join(dir, "reused.pid")
		other := sleeper(t).Process.Pid
		require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(other)), 0o600))

		pid, owned, err := Owner(path)
		require.NoError(t, err)
		assert.False(t, owned, "a live process the file names does not own it")
		assert.Equal(t, other, pid)
		assert.Equal(t, other, pidIn(t, path), "the probe changed the file")

		// The probe leaves no lock behind; RemoveFor removes what Owner
		// finds stale, while Acquire still refuses a file naming a live
		// process.
		_, err = Acquire(path, os.Getpid())
		require.ErrorIs(t, err, ErrBusy)

		removed, err := RemoveFor(path, pid, 0, nil)
		require.NoError(t, err)
		assert.True(t, removed)

		file, err := Acquire(path, os.Getpid())
		require.NoError(t, err)
		require.NoError(t, file.Release())
	})

	t.Run("an error", func(t *testing.T) {
		path := filepath.Join(dir, "directory.pid")
		require.NoError(t, os.Mkdir(path, 0o700))

		_, _, err := Owner(path)
		require.ErrorContains(t, err, "reading the pid file")
	})
}

// TestOwnerRacesAcquire has readers probe a pid file while acquirers take
// and release it: a reader never sees a file that an acquirer holds as
// stale, and an acquirer is refused only with ErrBusy. The acquirers do not
// retry a busy lock, as in TestExclusive.
func TestOwnerRacesAcquire(t *testing.T) {
	const (
		readers   = 4
		acquirers = 4
		attempts  = 300
	)

	path := filepath.Join(t.TempDir(), "x.pid")

	var (
		racers     sync.WaitGroup
		holding    atomic.Int32
		staleSeen  atomic.Int32
		unexpected = make(chan error, (readers+acquirers)*attempts)
	)

	for range acquirers {
		racers.Go(func() {
			for range attempts {
				file, err := acquire(path, os.Getpid(), 0)
				if err != nil {
					if !errors.Is(err, ErrBusy) {
						unexpected <- err
					}

					continue
				}

				holding.Add(1)
				time.Sleep(20 * time.Microsecond)
				holding.Add(-1)

				err = file.Release()
				if err != nil {
					unexpected <- err
				}
			}
		})
	}

	for range readers {
		racers.Go(func() {
			for range attempts {
				pid, owned, err := Owner(path)
				if err != nil {
					unexpected <- err

					continue
				}

				// A file this process wrote and nobody holds would be one
				// an owner left without removing it.
				if !owned && pid == os.Getpid() {
					staleSeen.Add(1)
				}
			}
		})
	}

	racers.Wait()
	close(unexpected)

	for err := range unexpected {
		require.NoError(t, err)
	}

	assert.Zero(t, staleSeen.Load(), "a file being released was seen as stale")
}

// TestBusyWait pins that Acquire and RemoveFor wait out a lock held for a
// moment, as Owner holds it to probe a file, and still refuse a file whose
// lock stays held, once busyWait is over.
func TestBusyWait(t *testing.T) {
	dir := t.TempDir()
	gone := deadPid(t)

	// holdLock holds the lock of the file at path through a descriptor of
	// its own, shared as Owner takes it or exclusive as an owner does, until
	// the returned function is called.
	holdLock := func(t *testing.T, path string, how int) func() {
		t.Helper()

		file, err := os.Open(path)
		require.NoError(t, err)
		require.NoError(t, syscall.Flock(int(file.Fd()), how))

		return func() { require.NoError(t, file.Close()) }
	}

	t.Run("a probe is waited out", func(t *testing.T) {
		path := filepath.Join(dir, "probed.pid")
		require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(gone)), 0o600))

		release := holdLock(t, path, syscall.LOCK_SH)
		time.AfterFunc(busyWait/4, release)

		file, err := Acquire(path, os.Getpid())
		require.NoError(t, err)
		require.NoError(t, file.Keep())

		// The file names this process now, which is not gone.
		release = holdLock(t, path, syscall.LOCK_SH)
		time.AfterFunc(busyWait/4, release)

		removed, err := RemoveFor(path, os.Getpid(), 0, nil)
		require.NoError(t, err)
		assert.True(t, removed)
	})

	t.Run("an owner is refused after busyWait", func(t *testing.T) {
		path := filepath.Join(dir, "owned.pid")
		require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(gone)), 0o600))

		release := holdLock(t, path, syscall.LOCK_EX)
		defer release()

		started := time.Now()
		_, err := Acquire(path, os.Getpid())
		require.ErrorIs(t, err, ErrBusy)
		assert.GreaterOrEqual(t, time.Since(started), busyWait)

		started = time.Now()

		removed, err := RemoveFor(path, gone, 0, nil)
		require.ErrorIs(t, err, ErrBusy)
		assert.False(t, removed)
		assert.GreaterOrEqual(t, time.Since(started), busyWait)
	})
}

// TestAcquireUnderProbes has a reader probe a pid file in a tight loop, as a
// tt instance status polled in a loop does, while the file is taken over and
// removed again and again: neither Acquire nor RemoveFor may take a probe
// for an owner.
func TestAcquireUnderProbes(t *testing.T) {
	const rounds = 300

	path := filepath.Join(t.TempDir(), "x.pid")
	gone := strconv.Itoa(deadPid(t))

	var (
		probing  sync.WaitGroup
		probes   atomic.Int64
		failures atomic.Int64
		stop     atomic.Bool
	)

	probing.Go(func() {
		for !stop.Load() {
			_, _, err := Owner(path)
			if err != nil {
				failures.Add(1)
			}

			probes.Add(1)
		}
	})

	defer func() {
		stop.Store(true)
		probing.Wait()
		assert.Zero(t, failures.Load(), "probes failed")
		t.Logf("%d probes", probes.Load())
	}()

	for round := range rounds {
		require.NoError(t, os.WriteFile(path, []byte(gone), 0o600))

		file, err := Acquire(path, os.Getpid())
		require.NoError(t, err, "round %d", round)

		// Left in place, the file names this process, unlocked.
		require.NoError(t, file.Keep())

		removed, err := RemoveFor(path, os.Getpid(), 0, nil)
		require.NoError(t, err, "round %d", round)
		assert.True(t, removed)
	}
}
