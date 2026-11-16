// Package pidfile implements the ownership of the pid files tt writes.
//
// A pid file holds the decimal pid without a newline, the format
// tt instance stop and tt instance status read; reading one never waits for
// its lock, and Owner tells a reader whether it is owned. Owning one means
// holding an exclusive flock on it, taken without blocking and kept for as
// long as the pid in it is current. The lock descriptor is close-on-exec, so
// no child inherits it, and the kernel drops it when the owner dies, however
// it dies.
//
// A pid file appears at its path whole and locked: Acquire writes the pid
// into a file of its own next to the path, locks it, and only then puts it
// at the path, by a link that fails when the path is taken, or by a rename
// over a stale file, done under the lock of that file. So a reader never
// finds the file of a new owner empty, nor unlocked: a file at the path that
// nobody holds is stale, and an owned one names its owner. A reader that does
// find an owned file without a pid, written by a tt older than this package
// or on a file system without links (see Acquire), takes it for an owner
// that is still starting (Owner).
//
// A file whose lock stays held is refused; a lock held for a moment, by
// Owner probing the file, is waited out. A file nobody holds is stale and is
// taken over, under its lock, unless it names a live process: that is either
// a writer that has exited and left the file to name the process it started
// (see File.Keep), or a tt that does not lock pid files. Of any number of
// processes racing for one pid file, exactly one owns it. An owner unlinks
// the file before it unlocks it, and a process that locked a file no longer
// at the path starts over, so ownership never passes to an unlinked file. A
// file is removed only by its owner, or under the lock by RemoveFor, and only
// while the path still names the file that was locked.
//
// The ownership is exclusive among the processes that follow this protocol,
// so every tt writer and remover of a pid file goes through this package. A
// process outside tt that writes or removes the same path, removes a file it
// has found stale without locking it, or removes the path unconditionally,
// is outside the guarantee and can unlink the file of a new owner.
package pidfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrBusy is wrapped by the error for a pid file that another running
	// process owns.
	ErrBusy = errors.New("the pid file belongs to a running process")
	// errUnstable reports a pid file replaced on every attempt to own it or
	// to tell its owner.
	errUnstable = errors.New("the pid file keeps being replaced")
	// errReplaced reports a pid file that the path no longer names once it
	// has been locked, so the attempt is repeated on what is there now.
	errReplaced = errors.New("the pid file has been replaced")
)

const (
	fileMode = 0o644
	dirMode  = 0o770
	// attempts bounds the retries when the path is replaced between opening
	// the file and locking it.
	attempts = 16
	// maxSize is as much of a pid file as is read; a pid is shorter.
	maxSize = 64
	// busyWait is how long Acquire and RemoveFor retry a busy lock before
	// they take the file for owned. Owner holds the lock for a few
	// microseconds to probe it, an owner for its whole life: an Acquire that
	// meets a probe, such as tt instance status polled while an instance
	// starts, gets the lock on a retry, and an owned file is refused busyWait
	// late.
	busyWait = 200 * time.Millisecond
	// retryInterval is the pause between attempts to lock a busy file: long
	// next to a probe, short next to busyWait.
	retryInterval = 2 * time.Millisecond
)

// File is a pid file this process owns.
type File struct {
	path string
	file *os.File
}

// Acquire takes ownership of the pid file at path and writes pid into it,
// creating the directory if needed. A file another process owns is refused
// with an error wrapping ErrBusy, once its lock has stayed busy for busyWait:
// a lock held for a moment, by Owner probing the file, is no owner.
//
// The file is written and locked before it is at the path (see the package
// documentation). A file system that has no links gets it the way tt wrote
// pid files before: created at the path, then locked and written, which
// leaves a reader a moment in which the file is there without a pid.
func Acquire(path string, pid int) (*File, error) {
	return acquire(path, pid, busyWait)
}

// acquire is Acquire that retries a busy lock for up to wait.
func acquire(path string, pid int, wait time.Duration) (*File, error) {
	err := os.MkdirAll(filepath.Dir(path), dirMode)
	if err != nil {
		return nil, fmt.Errorf("creating the pid file directory: %w", err)
	}

	next, err := newPending(path, pid)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(wait)

	for range attempts {
		owned, err := next.place(deadline)

		switch {
		case errors.Is(err, errReplaced):
			continue
		case err != nil:
			return nil, errors.Join(err, next.discard())
		}

		return owned, nil
	}

	return nil, errors.Join(fmt.Errorf("%w: %s", errUnstable, path), next.discard())
}

// pending is the file a new owner puts at the path of its pid file: written
// and locked under a name of its own in the same directory.
type pending struct {
	path string
	pid  int
	// file is the pending file, locked; name is its name while it is not
	// at path yet.
	file *os.File
	name string
	// linkless is set once the file system has refused a link: the file is
	// created at the path instead (acquireInPlace).
	linkless bool
}

// newPending writes pid into a new file next to path and locks it.
func newPending(path string, pid int) (*pending, error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return nil, fmt.Errorf("creating the pid file: %w", err)
	}

	next := &pending{path: path, pid: pid, file: file, name: file.Name(), linkless: false}

	err = os.Chmod(next.name, fileMode)
	if err == nil {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}

	if err == nil {
		_, err = file.WriteAt([]byte(strconv.Itoa(pid)), 0)
	}

	if err != nil {
		return nil, errors.Join(fmt.Errorf("writing the pid file: %w", err), next.discard())
	}

	return next, nil
}

// discard removes the pending file that has not been put at the path.
func (next *pending) discard() error {
	if next.file == nil {
		return nil
	}

	err := os.Remove(next.name)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}

	err = errors.Join(err, closeFile(next.file))
	next.file = nil

	return err
}

// place makes one attempt to put the pending file at the path: a link,
// which only a missing path takes, or a rename over a stale file under its
// lock. It returns an error wrapping errReplaced when the path changed on
// the way, so the attempt has to be repeated on what is there now.
func (next *pending) place(deadline time.Time) (*File, error) {
	if next.linkless {
		return next.acquireInPlace(deadline)
	}

	err := os.Link(next.name, next.path)

	switch {
	case err == nil:
		return next.owned(), nil
	case errors.Is(err, fs.ErrExist):
		return next.replaceStale(deadline)
	default:
		// No links here, such as on some network and FUSE file systems.
		next.linkless = true

		return next.acquireInPlace(deadline)
	}
}

// owned gives up the name of the pending file, now at the path, and returns
// it as the owned pid file.
func (next *pending) owned() *File {
	_ = os.Remove(next.name)

	return next.handOver()
}

// handOver returns the pending file, now at the path, as the owned pid file.
func (next *pending) handOver() *File {
	owned := &File{path: next.path, file: next.file}

	next.file = nil

	return owned
}

// replaceStale renames the pending file over the file at the path, if that
// one is stale, while it holds the lock of that file: whoever locks it after
// that finds it no longer at the path, and starts over on the new file.
func (next *pending) replaceStale(deadline time.Time) (*File, error) {
	file, err := os.OpenFile(next.path, os.O_RDWR, 0)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, errReplaced
	case err != nil:
		return nil, fmt.Errorf("opening the pid file: %w", err)
	}

	// Closing releases the lock of the stale file, once the new one is in
	// its place.
	defer func() { _ = file.Close() }()

	err = checkStale(file, next.path, next.pid, deadline)
	if err != nil {
		return nil, err
	}

	err = os.Rename(next.name, next.path)
	if err != nil {
		return nil, fmt.Errorf("replacing the stale pid file: %w", err)
	}

	return next.handOver(), nil
}

// checkStale locks file, opened at path, and checks that it is still the
// file there, or fails with errReplaced, and that it is stale: it names no
// live process other than pid. A file naming another live process is
// refused with ErrBusy.
func checkStale(file *os.File, path string, pid int, deadline time.Time) error {
	err := lock(file, path, deadline)
	if err != nil {
		return err
	}

	// The previous owner unlinks the file before it unlocks it: a file locked
	// after that is no longer the one at path.
	same, err := stillAt(file, path)

	switch {
	case err != nil:
		return err
	case !same:
		return errReplaced
	}

	holder, err := readPid(file)
	if err != nil {
		return err
	}

	if holder != pid && alive(holder) {
		return fmt.Errorf("%w %d: %s", ErrBusy, holder, path)
	}

	return nil
}

// acquireInPlace makes one attempt to own the file at the path the way tt
// did before it put pid files there whole: it creates the file, then locks
// it and writes the pid into it, for a file system that has no links.
func (next *pending) acquireInPlace(deadline time.Time) (*File, error) {
	file, err := os.OpenFile(next.path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("opening the pid file: %w", err)
	}

	err = checkStale(file, next.path, next.pid, deadline)
	if err == nil {
		err = file.Truncate(0)
		if err == nil {
			_, err = file.WriteAt([]byte(strconv.Itoa(next.pid)), 0)
		}

		if err == nil {
			// The pending file is not needed any more; one left behind is
			// a stray file next to the pid file, not a failure to own it.
			_ = next.discard()

			return &File{path: next.path, file: file}, nil
		}

		err = fmt.Errorf("writing the pid file: %w", err)
	}

	// Closing releases the lock, if it was taken.
	return nil, errors.Join(err, closeFile(file))
}

// lock takes the exclusive lock of file, retrying a busy lock every
// retryInterval until deadline.
func lock(file *os.File, path string, deadline time.Time) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)

		switch {
		case err == nil:
			return nil
		case !errors.Is(err, syscall.EWOULDBLOCK):
			return fmt.Errorf("locking the pid file: %w", err)
		case !time.Now().Before(deadline):
			return fmt.Errorf("%w: %s", ErrBusy, path)
		}

		time.Sleep(retryInterval)
	}
}

// stillAt reports whether path names the open file. A missing path does not.
func stillAt(file *os.File, path string) (bool, error) {
	opened, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("checking the pid file: %w", err)
	}

	named, err := os.Stat(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("checking the pid file: %w", err)
	}

	return os.SameFile(opened, named), nil
}

// readPid reads the pid in a file; 0 when it holds none.
func readPid(file *os.File) (int, error) {
	data, err := io.ReadAll(io.NewSectionReader(file, 0, maxSize))
	if err != nil {
		return 0, fmt.Errorf("reading the pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, nil //nolint:nilerr // Unreadable content means no owner.
	}

	return pid, nil
}

// alive reports whether a process with pid exists, even one this process may
// not signal.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)

	return err == nil || errors.Is(err, syscall.EPERM)
}

// Alive returns the pid the file at path names and whether that process is
// alive, judged as Acquire judges it before taking a file over: a process
// this one may not signal is alive too. A missing file, or one that names no
// pid, names no live process.
//
// The file is read without the lock, so the answer can be out of date by the
// time the caller acts on it, and it is not for deciding who owns the file.
// It is for a file whose lock does not follow the process it names: one left
// by File.Keep, or the pid file of a child its owner wrote, which the child
// may outlive, such as process.pid of an external process. Whether the
// process that owns a file runs, such as the watchdog of an instance, Owner
// tells: the pid in a file its owner left may name another process by now.
func Alive(path string) (int, bool, error) {
	file, err := os.Open(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("opening the pid file: %w", err)
	}

	pid, err := readPid(file)

	return pid, alive(pid), errors.Join(err, closeFile(file))
}

// Owner returns the pid the file at path names and whether a process owns
// the file, holding its lock. A missing file names no pid and is owned by
// nobody; so does a file that holds no pid, unless its lock is held.
//
// A file nobody holds is stale whatever process the pid in it names now:
// the kernel drops the lock of an owner however it dies, while the file
// stays, and its pid may have been taken since by an unrelated process,
// after a reboot above all. For a pid file every writer of which owns it for
// as long as the pid in it is current, such as the pid file of the watchdog
// of an instance, a file nobody holds means that its process is gone, which
// Alive cannot tell. That does not hold for a file left by File.Keep: it is
// unlocked while the process it names runs.
//
// The lock is probed without waiting: a shared lock taken on a descriptor
// opened for reading and dropped at once. The pid of a file nobody holds is
// read under that lock, which no owner writes past, and only once the path
// is checked to still name the file locked: a file its owner has just
// released counts as missing rather than stale. The pid of an owned file is
// read without the lock, as Alive reads it.
//
// The lock, not the pid, tells whether the file is owned. An owned file
// holds its pid from the moment it is at the path (Acquire), except one
// written the way tt wrote pid files before, which an owner that is still
// starting may hold without a pid for a moment: Owner gives it the moment to
// write it, and returns pid 0 with owned if it still has none. A caller
// takes that for an owner that is starting, never for a process to signal.
//
// The probe holds the lock for a moment, which Acquire and RemoveFor wait
// out rather than take the file for owned (busyWait), so probing a file
// does not keep a process from owning it. The answer can be out of date by
// the time the caller acts on it: an owner may come or go right after. A
// caller that removes a stale file does it with RemoveFor, which checks the
// file again under the lock.
func Owner(path string) (int, bool, error) {
	for range attempts {
		pid, owned, err := probeOwner(path)
		if !errors.Is(err, errReplaced) {
			return pid, owned, err
		}
	}

	return 0, false, fmt.Errorf("%w: %s", errUnstable, path)
}

// probeOwner makes one attempt. It returns an error wrapping errReplaced when
// the path was replaced or removed after it was opened, so the attempt has
// to be repeated on what is at the path now.
func probeOwner(path string) (int, bool, error) {
	file, err := os.Open(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("opening the pid file: %w", err)
	}

	pid, owned, err := probeLock(file, path)

	// Closing releases the shared lock, if it was taken.
	return pid, owned, errors.Join(err, closeFile(file))
}

func probeLock(file *os.File, path string) (int, bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)

	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		return readOwnedPid(file)
	case err != nil:
		return 0, false, fmt.Errorf("probing the pid file lock: %w", err)
	}

	same, err := stillAt(file, path)

	switch {
	case err != nil:
		return 0, false, err
	case !same:
		return 0, false, errReplaced
	}

	pid, err := readPid(file)

	return pid, false, err
}

// readOwnedPid reads the pid of file, which its owner holds locked. An owner
// that writes the pid after it has locked the file (acquireInPlace) gets
// busyWait to write it; the pid is 0 if it has not by then.
func readOwnedPid(file *os.File) (int, bool, error) {
	deadline := time.Now().Add(busyWait)

	for {
		pid, err := readPid(file)
		if err != nil || pid != 0 || !time.Now().Before(deadline) {
			return pid, true, err
		}

		time.Sleep(retryInterval)
	}
}

// Release removes the pid file, if the path still names it, and gives up the
// ownership. The file is unlinked before it is unlocked, so a process that
// locks it afterwards sees that it is gone.
func (owned *File) Release() error {
	same, err := stillAt(owned.file, owned.path)
	if err == nil && same {
		err = os.Remove(owned.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			err = fmt.Errorf("removing the pid file: %w", err)
		} else {
			err = nil
		}
	}

	return errors.Join(err, closeFile(owned.file))
}

// Reserve takes ownership of the pid file at path, as Acquire does, before
// the pid it is to hold is known: the file holds no pid until SetPid writes
// it, and Owner tells a reader that it is owned by a process that is
// starting. It is for a process that starts another one and hands it the
// lock (Lock), so that the pid file is owned by that process from its first
// moment.
func Reserve(path string) (*File, error) {
	return acquire(path, 0, busyWait)
}

// SetPid writes pid into the owned file, in place of what it holds: over
// the old content first, then cutting off the rest, so that a reader never
// finds the file empty.
func (owned *File) SetPid(pid int) error {
	data := []byte(strconv.Itoa(pid))

	_, err := owned.file.WriteAt(data, 0)
	if err == nil {
		err = owned.file.Truncate(int64(len(data)))
	}

	if err != nil {
		return fmt.Errorf("writing the pid file: %w", err)
	}

	return nil
}

// Lock returns the open file that holds the lock, for a child process to
// inherit through exec.Cmd.ExtraFiles: the lock belongs to the open file,
// which the child shares, so the child holds the file after this process
// has let it go (Keep), until the child exits.
func (owned *File) Lock() *os.File {
	return owned.file
}

// Keep gives up the ownership and leaves the file in place, for a writer
// that exits while the process it wrote down goes on. A file whose lock that
// process has inherited (Lock) stays owned by it. Any other is protected from
// then on only by the pid in it: Acquire refuses a file that names a live
// process, and takes it over once that process is gone.
func (owned *File) Keep() error {
	return closeFile(owned.file)
}

// RemoveFor removes the pid file at path if it still names pid, taking the
// lock first. It is for the pid file of a process that is gone, killed or
// not, whose lock the kernel drops once the process has exited: it retries
// the lock for up to wait, and for busyWait at least, as Acquire does. It
// returns whether it removed the file. A missing file, or one that names
// another process, is left as it is: that is a file a newer owner wrote.
//
// Before it removes the file, still holding the lock, RemoveFor runs a non-nil
// cleanup to remove what else the gone process left behind: no newer owner
// can exist until the file is gone, so nothing cleanup finds belongs to one.
// It does not run cleanup when it leaves the file.
func RemoveFor(path string, pid int, wait time.Duration, cleanup func()) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("opening the pid file: %w", err)
	}

	removed, err := removeLocked(file, path, pid, wait, cleanup)

	return removed, errors.Join(err, closeFile(file))
}

func removeLocked(file *os.File, path string, pid int, wait time.Duration,
	cleanup func(),
) (bool, error) {
	err := lock(file, path, time.Now().Add(max(wait, busyWait)))
	if err != nil {
		return false, err
	}

	same, err := stillAt(file, path)
	if err != nil || !same {
		return false, err
	}

	holder, err := readPid(file)
	if err != nil || holder != pid {
		return false, err
	}

	if cleanup != nil {
		cleanup()
	}

	err = os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("removing the pid file: %w", err)
	}

	return true, nil
}

func closeFile(file *os.File) error {
	err := file.Close()
	if err != nil {
		return fmt.Errorf("closing the pid file: %w", err)
	}

	return nil
}
