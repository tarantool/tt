package projectlock

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helperEnv makes the test binary a helper process (TestHelperProcess):
// "hold DIR" takes the lock of DIR, says "held" and keeps it until its stdin
// is closed; "try DIR" takes it and says "free" when it did not wait,
// "waited" when it did.
const helperEnv = "TT_PROJECTLOCK_HELPER"

// TestHelperProcess is the helper process of the tests, run by helper; a
// test run without helperEnv skips it.
//
//nolint:paralleltest // A process of its own.
func TestHelperProcess(t *testing.T) {
	mode, dir, ok := strings.Cut(os.Getenv(helperEnv), " ")
	if !ok {
		t.Skip("run by the other tests")
	}

	waited := false

	_, release, err := acquire(context.Background(), dir, func(int, bool) { waited = true })
	if err != nil {
		_, _ = os.Stdout.WriteString("error " + err.Error() + "\n")

		os.Exit(1)
	}

	switch mode {
	case "hold":
		_, _ = os.Stdout.WriteString("held\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "try":
		if waited {
			_, _ = os.Stdout.WriteString("waited\n")
		} else {
			_, _ = os.Stdout.WriteString("free\n")
		}
	}

	release()
	os.Exit(0)
}

// helper is a helper process of the test binary.
type helper struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines *bufio.Scanner
}

// startHelper starts a helper process in mode on dir, with env added to the
// environment of this process.
func startHelper(t *testing.T, mode, dir string, env ...string) *helper {
	t.Helper()

	//nolint:gosec // The test binary runs itself.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHelperProcess$")

	cmd.Env = append(append(os.Environ(), helperEnv+"="+mode+" "+dir), env...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	return &helper{cmd: cmd, stdin: stdin, lines: bufio.NewScanner(stdout)}
}

// line is the next line the helper says.
func (h *helper) line(t *testing.T) string {
	t.Helper()

	require.True(t, h.lines.Scan(), "the helper said nothing: %v", h.lines.Err())

	return h.lines.Text()
}

// project makes a directory with a manifest.
func project(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName),
		[]byte("[package]\nname = \"demo\"\n"), 0o600))

	return dir
}

// noWait fails the test when an acquire waits.
func noWait(t *testing.T) Waiting {
	t.Helper()

	return func(pid int, self bool) {
		t.Errorf("waited for pid %d (self: %v)", pid, self)
	}
}

// TestReentrantByContext takes the lock with the context that carries it:
// nothing is taken again, and the release of the inner one releases
// nothing.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestReentrantByContext(t *testing.T) {
	dir := project(t)

	ctx, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)
	assert.True(t, Held(ctx, dir))
	assert.False(t, Held(t.Context(), dir))

	inner, innerRelease, err := acquire(ctx, dir, noWait(t))
	require.NoError(t, err)
	innerRelease()
	assert.True(t, Held(inner, dir))

	// A spelling through a link is the same project.
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))

	_, linkRelease, err := acquire(ctx, link, noWait(t))
	require.NoError(t, err)
	linkRelease()

	// The lock is still held: another process has to wait.
	assert.Contains(t, os.Getenv(EnvVar), ":")

	release()
	release()

	assert.Empty(t, os.Getenv(EnvVar))

	data, err := os.ReadFile(Path(dir))
	require.NoError(t, err)
	assert.Empty(t, data, "a released lock names no holder")
}

// TestGoroutinesWait has a goroutine with another context wait for the lock
// this one holds, told that the holder is this process.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestGoroutinesWait(t *testing.T) {
	dir := project(t)

	_, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)

	var self atomic.Bool

	done := make(chan error, 1)

	go func() {
		_, second, err := acquire(context.Background(), dir, func(_ int, isSelf bool) {
			self.Store(isSelf)
		})
		if err == nil {
			second()
		}

		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("the second acquire did not wait: %v", err)
	case <-time.After(3 * pollInterval):
	}

	release()
	require.NoError(t, <-done)
	assert.True(t, self.Load())
}

// TestOtherProcessWaits has this process wait for the lock a helper holds,
// told its pid, until the context is done, then take the lock the helper
// released.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestOtherProcessWaits(t *testing.T) {
	dir := project(t)

	holder := startHelper(t, "hold", dir)
	require.Equal(t, "held", holder.line(t))

	var waitedFor atomic.Int64

	ctx, cancel := context.WithTimeout(t.Context(), 3*pollInterval)
	defer cancel()

	_, _, err := acquire(ctx, dir, func(pid int, self bool) {
		assert.False(t, self)
		waitedFor.Store(int64(pid))
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, int64(holder.cmd.Process.Pid), waitedFor.Load())

	// The semaphore of the process is free again after a failed wait.
	require.NoError(t, holder.stdin.Close())
	require.NoError(t, holder.cmd.Wait())

	_, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)
	release()
}

// holdUnnamed locks the lock file of the project in dir as a tt has it in
// the instant between its flock(2) and the write of its pid, and just before
// it gives the lock up: held, naming no holder. Closing the file returned
// releases it.
func holdUnnamed(t *testing.T, dir string) *os.File {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, BuildDirName), dirPerm))

	file, err := os.OpenFile(Path(dir), os.O_RDWR|os.O_CREATE, filePerm)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })

	require.NoError(t, syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))

	return file
}

// waitInBackground acquires the lock of the project in dir in a goroutine,
// releasing it at once, and returns the pids its wait was told of and the
// result.
func waitInBackground(t *testing.T, dir string) (chan int, chan error) {
	t.Helper()

	told := make(chan int, unnamedPolls)
	done := make(chan error, 1)

	go func() {
		_, release, err := acquire(context.Background(), dir, func(pid int, self bool) {
			assert.False(t, self)

			told <- pid
		})
		if err == nil {
			release()
		}

		done <- err
	}()

	return told, done
}

// TestWaitTellsTheHolderNamedLate has this process wait for a holder that
// writes its pid after the wait first reads the file: the wait is told once,
// of that pid, not of a holder the file does not name.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestWaitTellsTheHolderNamedLate(t *testing.T) {
	dir := project(t)
	holder := holdUnnamed(t, dir)

	told, done := waitInBackground(t, dir)

	time.Sleep(3 * pollInterval / 2)

	_, err := holder.WriteAt([]byte("4242\n"), 0)
	require.NoError(t, err)

	time.Sleep(3 * pollInterval)
	require.NoError(t, holder.Close())
	require.NoError(t, <-done)

	require.Len(t, told, 1)
	assert.Equal(t, 4242, <-told)
}

// TestWaitTellsNothingOfAHolderGivingUp: a lock held with no pid that is
// free at a poll after, as a tt gives it up, is taken with nothing told.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestWaitTellsNothingOfAHolderGivingUp(t *testing.T) {
	dir := project(t)
	holder := holdUnnamed(t, dir)

	told, done := waitInBackground(t, dir)

	time.Sleep(3 * pollInterval / 2)
	require.NoError(t, holder.Close())
	require.NoError(t, <-done)

	assert.Empty(t, told)
}

// TestWaitTellsAHolderThatIsNotATt: a holder the file names for none of
// unnamedPolls polls is told with no pid.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestWaitTellsAHolderThatIsNotATt(t *testing.T) {
	dir := project(t)
	holder := holdUnnamed(t, dir)

	told, done := waitInBackground(t, dir)

	select {
	case pid := <-told:
		assert.Equal(t, 0, pid)
	case <-time.After((unnamedPolls + 5) * pollInterval):
		require.FailNow(t, "the wait was told of no holder")
	}

	require.NoError(t, holder.Close())
	require.NoError(t, <-done)
	assert.Empty(t, told, "the wait is told once")
}

// TestCrashReleases kills the helper holding the lock: the kernel drops the
// lock, and the next acquire takes the file it left at once.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestCrashReleases(t *testing.T) {
	dir := project(t)

	holder := startHelper(t, "hold", dir)
	require.Equal(t, "held", holder.line(t))

	require.NoError(t, holder.cmd.Process.Signal(syscall.SIGKILL))

	_ = holder.cmd.Wait()

	data, err := os.ReadFile(Path(dir))
	require.NoError(t, err)
	assert.NotEmpty(t, data, "the crashed holder left its pid")

	_, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)
	release()
}

// TestChildWorksUnderTheLock runs a helper while this process holds the
// lock: it inherits the environment, and works under the lock rather than
// wait for it. A helper that does not inherit it waits.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestChildWorksUnderTheLock(t *testing.T) {
	dir := project(t)

	_, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)

	child := startHelper(t, "try", dir)
	assert.Equal(t, "free", child.line(t))
	require.NoError(t, child.cmd.Wait())

	// The lock is still this process's: the child released nothing.
	data, err := os.ReadFile(Path(dir))
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid())+"\n", string(data))

	stranger := startHelper(t, "try", dir, EnvVar+"=")

	time.Sleep(3 * pollInterval)
	release()
	assert.Equal(t, "waited", stranger.line(t))
}

// TestStaleEnvironment gives a helper the entry of a lock this process no
// longer holds: it takes the lock as any process does, and a holder it
// does not name is waited for.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestStaleEnvironment(t *testing.T) {
	dir := project(t)

	_, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)

	stale := os.Getenv(EnvVar)

	release()

	holder := startHelper(t, "hold", dir, EnvVar+"=")
	require.Equal(t, "held", holder.line(t))

	// The entry names this process, which holds nothing: the child waits
	// for the helper.
	child := startHelper(t, "try", dir, EnvVar+"="+stale)

	time.Sleep(3 * pollInterval)
	require.NoError(t, holder.stdin.Close())
	assert.Equal(t, "waited", child.line(t))
}

// TestNoManifestNoLock takes no lock of a directory without a manifest, and
// creates nothing in it.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestNoManifestNoLock(t *testing.T) {
	dir := t.TempDir()

	ctx, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)
	release()

	assert.False(t, Held(ctx, dir))
	assert.NoDirExists(t, filepath.Join(dir, BuildDirName))
}

// TestBuildDirRemoved removes the build directory under a lock held: the
// next acquire takes the file made anew, not the unlinked one.
//
//nolint:paralleltest // The lock sets the environment of the process.
func TestBuildDirRemoved(t *testing.T) {
	dir := project(t)

	_, release, err := acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)
	release()

	require.NoError(t, os.RemoveAll(filepath.Join(dir, BuildDirName)))

	_, release, err = acquire(t.Context(), dir, noWait(t))
	require.NoError(t, err)

	data, err := os.ReadFile(Path(dir))
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	release()
}
