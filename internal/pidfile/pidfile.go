// Package pidfile implements the ownership of the pid files tt writes.
//
// A pid file holds the decimal pid without a newline, the format tt stop and
// tt status read; reading one takes no lock. Owning one means holding an
// exclusive flock on it, taken without waiting and kept for as long as the
// pid in it is current. The lock descriptor is close-on-exec, so no child
// inherits it, and the kernel drops it when the owner dies, however it dies.
//
// A file whose lock is held is refused. A file nobody holds is stale and is
// taken over in place, under the lock, unless it names a live process: that
// is either a writer that has exited and left the file to name the process
// it started (see File.Keep), or a tt that does not lock pid files. Of any
// number of processes racing for one pid file, exactly one owns it. An owner
// unlinks the file before it unlocks it, and a process that locked a file no
// longer at the path starts over, so ownership never passes to an unlinked
// file. A file is removed only by its owner, or under the lock by RemoveFor,
// and only while the path still names the file that was locked.
//
// The ownership is exclusive among the processes that follow this protocol.
// A writer or remover of the same path that does not, one that removes a
// file it has found stale without locking it, or removes the path
// unconditionally, can unlink the file of a new owner and break it.
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
	// errUnstable reports a pid file replaced on every attempt to own it.
	errUnstable = errors.New("the pid file keeps being replaced")
)

const (
	fileMode = 0o644
	dirMode  = 0o770
	// attempts bounds the retries when the path is replaced between opening
	// the file and locking it.
	attempts = 16
	// maxSize is as much of a pid file as is read; a pid is shorter.
	maxSize = 64
	// retryInterval is the pause between attempts to lock in RemoveFor.
	retryInterval = 10 * time.Millisecond
)

// File is a pid file this process owns.
type File struct {
	path string
	file *os.File
}

// Acquire takes ownership of the pid file at path and writes pid into it,
// creating the directory if needed. A file another process owns is refused
// with an error wrapping ErrBusy.
func Acquire(path string, pid int) (*File, error) {
	err := os.MkdirAll(filepath.Dir(path), dirMode)
	if err != nil {
		return nil, fmt.Errorf("creating the pid file directory: %w", err)
	}

	for range attempts {
		owned, err := tryAcquire(path, pid)
		if owned != nil || err != nil {
			return owned, err
		}
	}

	return nil, fmt.Errorf("%w: %s", errUnstable, path)
}

// tryAcquire makes one attempt. It returns neither a file nor an error when
// the path was replaced after it was opened, so the attempt has to be
// repeated on the new file.
func tryAcquire(path string, pid int) (*File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("opening the pid file: %w", err)
	}

	owned, err := lockAndWrite(file, path, pid)
	if owned == nil {
		// Closing releases the lock, if it was taken.
		err = errors.Join(err, closeFile(file))
	}

	return owned, err
}

func lockAndWrite(file *os.File, path string, pid int) (*File, error) {
	err := lock(file, path)
	if err != nil {
		return nil, err
	}

	// The previous owner unlinks the file before it unlocks it: a file locked
	// after that is no longer the one at path.
	same, err := stillAt(file, path)
	if err != nil || !same {
		return nil, err
	}

	holder, err := readPid(file)
	if err != nil {
		return nil, err
	}

	if holder != pid && alive(holder) {
		return nil, fmt.Errorf("%w %d: %s", ErrBusy, holder, path)
	}

	err = file.Truncate(0)
	if err != nil {
		return nil, fmt.Errorf("truncating the pid file: %w", err)
	}

	_, err = file.WriteAt([]byte(strconv.Itoa(pid)), 0)
	if err != nil {
		return nil, fmt.Errorf("writing the pid file: %w", err)
	}

	return &File{path: path, file: file}, nil
}

func lock(file *os.File, path string) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)

	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		return fmt.Errorf("%w: %s", ErrBusy, path)
	case err != nil:
		return fmt.Errorf("locking the pid file: %w", err)
	}

	return nil
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

func (owned *File) Path() string {
	return owned.path
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

// Keep gives up the ownership and leaves the file in place, for a writer
// that exits while the process it wrote down goes on. The file is protected
// from then on only by the pid in it: Acquire refuses a file that names a
// live process, and takes it over once that process is gone.
func (owned *File) Keep() error {
	return closeFile(owned.file)
}

// RemoveFor removes the pid file at path if it still names pid, taking the
// lock first. It is for the pid file of a process that is gone, killed or
// not, whose lock the kernel drops once the process has exited: it retries
// the lock for up to wait. It returns whether it removed the file. A missing
// file, or one that names another process, is left as it is: that is a file
// a newer owner wrote.
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
	deadline := time.Now().Add(wait)

	for {
		err := lock(file, path)
		if err == nil {
			break
		}

		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			return false, err
		}

		time.Sleep(retryInterval)
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
