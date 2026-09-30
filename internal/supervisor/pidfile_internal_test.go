package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acquired is the outcome of one attempt to own a pid file.
type acquired struct {
	owned *pidFile
	err   error
}

// TestPidFileStaleTakeover has many acquirers race for one stale pid file,
// round after round: exactly one of them may own it.
func TestPidFileStaleTakeover(t *testing.T) {
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

				owned, err := acquirePidFile(path, os.Getpid())
				results <- acquired{owned: owned, err: err}
			})
		}

		start.Done()
		racers.Wait()
		close(results)

		var winners []*pidFile

		for result := range results {
			if result.err != nil {
				require.ErrorIs(t, result.err, ErrPidFileBusy)

				continue
			}

			winners = append(winners, result.owned)
		}

		require.Len(t, winners, 1, "round %d", round)
		assert.Equal(t, os.Getpid(), readPid(t, path))
		require.NoError(t, winners[0].release())
		assert.NoFileExists(t, path)
	}
}

// TestPidFileExclusive has acquirers take, hold and release one pid file in
// a loop: however acquiring and releasing interleave, no two of them may own
// it at once.
func TestPidFileExclusive(t *testing.T) {
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
				file, err := acquirePidFile(path, os.Getpid())
				if err != nil {
					if !errors.Is(err, ErrPidFileBusy) {
						unexpected <- err
					}

					continue
				}

				owned.Add(1)

				if owners.Add(1) != 1 {
					overlap.Add(1)
				}

				// While it is owned, nobody can replace it: the owner's file
				// is the one tt stop and tt status read.
				same, err := stillAt(file.file, path)
				if err != nil || !same {
					orphaned.Add(1)
				}

				time.Sleep(20 * time.Microsecond)
				owners.Add(-1)

				err = file.release()
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

// TestPidFileLockNotInherited pins that a child does not inherit the lock:
// once the owner is gone, the file can be taken over while the child still
// runs.
func TestPidFileLockNotInherited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.pid")

	owned, err := acquirePidFile(path, os.Getpid())
	require.NoError(t, err)

	spec := helperSpec(t, dir, modeServe)
	cmd := command(t.Context(), &spec)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	waitReady(t, dir, cmd.Process.Pid)

	// The owner dies without removing the file.
	require.NoError(t, owned.file.Close())

	again, err := acquirePidFile(path, os.Getpid())
	require.NoError(t, err, "the child holds the lock")
	require.NoError(t, again.release())
}

// TestPidFileLocklessOwner pins that a pid file written without a lock is
// still refused while the process it names runs, and taken over once that
// process is gone.
func TestPidFileLocklessOwner(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.pid")

	spec := helperSpec(t, dir, modeServe)
	cmd := command(t.Context(), &spec)
	require.NoError(t, cmd.Start())

	other := strconv.Itoa(cmd.Process.Pid)
	require.NoError(t, os.WriteFile(path, []byte(other), 0o600))

	_, err := acquirePidFile(path, os.Getpid())
	require.ErrorIs(t, err, ErrPidFileBusy)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, other, string(data))

	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait())

	owned, err := acquirePidFile(path, os.Getpid())
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), readPid(t, path))
	require.NoError(t, owned.release())
}

// TestPidFileRelease pins that releasing removes only the file this owner
// locked, not one that replaced it.
func TestPidFileRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.pid")

	owned, err := acquirePidFile(path, os.Getpid())
	require.NoError(t, err)
	require.NoError(t, owned.release())
	assert.NoFileExists(t, path)

	owned, err = acquirePidFile(path, os.Getpid())
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.WriteFile(path, []byte("42"), 0o600))
	require.NoError(t, owned.release())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "42", string(data), "another file at the path was removed")
}
