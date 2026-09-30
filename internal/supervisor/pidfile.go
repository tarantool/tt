package supervisor

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
)

var (
	// ErrPidFileBusy is wrapped by the error for a pid file another running
	// process owns.
	ErrPidFileBusy = errors.New("the pid file belongs to a running process")
	// errPidFileUnstable reports a pid file replaced on every attempt to own
	// it.
	errPidFileUnstable = errors.New("the pid file keeps being replaced")
)

const (
	// pidFileMode is the mode of a pid file tt creates.
	pidFileMode = 0o644
	// pidDirMode is the mode of a directory created for a pid file.
	pidDirMode = 0o770
	// pidFileAttempts bounds the retries when the path is replaced between
	// opening the file and locking it.
	pidFileAttempts = 16
	// pidFileMaxSize is as much of a pid file as is read; a pid is shorter.
	pidFileMaxSize = 64
)

// pidFile is a pid file this process owns: it holds an exclusive flock on
// the open file for as long as it owns it. The descriptor is close-on-exec,
// as every file Go opens, so a child never inherits the lock, and the kernel
// releases it when the owner dies, however it dies.
type pidFile struct {
	path string
	file *os.File
}

// acquirePidFile takes ownership of the pid file at path and writes pid into
// it, in the format tt reads: the decimal pid without a newline.
//
// Ownership is the lock: a file whose lock is held belongs to its holder. A
// file nobody holds is stale and is taken over in place, unless it names a
// live process other than pid: such a file was written by a tt that does not
// lock pid files, and its process is still running.
func acquirePidFile(path string, pid int) (*pidFile, error) {
	err := os.MkdirAll(filepath.Dir(path), pidDirMode)
	if err != nil {
		return nil, fmt.Errorf("creating the pid file directory: %w", err)
	}

	for range pidFileAttempts {
		owned, err := tryAcquirePidFile(path, pid)
		if owned != nil || err != nil {
			return owned, err
		}
	}

	return nil, fmt.Errorf("%w: %s", errPidFileUnstable, path)
}

// tryAcquirePidFile makes one attempt. It returns neither a file nor an error
// when the path was replaced after it was opened, so the attempt has to be
// repeated on the new file.
func tryAcquirePidFile(path string, pid int) (*pidFile, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, pidFileMode)
	if err != nil {
		return nil, fmt.Errorf("opening the pid file: %w", err)
	}

	owned, err := lockPidFile(file, path, pid)
	if owned == nil {
		// Closing releases the lock, if it was taken.
		err = errors.Join(err, closePidFile(file))
	}

	return owned, err
}

// lockPidFile locks the open file and, once it owns it, writes pid.
func lockPidFile(file *os.File, path string, pid int) (*pidFile, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)

	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		return nil, fmt.Errorf("%w: %s", ErrPidFileBusy, path)
	case err != nil:
		return nil, fmt.Errorf("locking the pid file: %w", err)
	}

	// The previous owner unlinks the file before it unlocks it: a file locked
	// after that is no longer the one at path.
	same, err := stillAt(file, path)
	if err != nil || !same {
		return nil, err
	}

	holder, err := lockedPid(file)
	if err != nil {
		return nil, err
	}

	if holder != pid && alive(holder) {
		return nil, fmt.Errorf("%w %d: %s", ErrPidFileBusy, holder, path)
	}

	err = file.Truncate(0)
	if err != nil {
		return nil, fmt.Errorf("truncating the pid file: %w", err)
	}

	_, err = file.WriteAt([]byte(strconv.Itoa(pid)), 0)
	if err != nil {
		return nil, fmt.Errorf("writing the pid file: %w", err)
	}

	return &pidFile{path: path, file: file}, nil
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

// lockedPid reads the pid in a file; 0 when it holds none.
func lockedPid(file *os.File) (int, error) {
	data, err := io.ReadAll(io.NewSectionReader(file, 0, pidFileMaxSize))
	if err != nil {
		return 0, fmt.Errorf("reading the pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		// Empty or garbage, and nobody holds the lock: nothing to keep.
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

// release removes the pid file, if the path still names it, and gives up the
// ownership. The file is unlinked before it is unlocked, so a process that
// locks it afterwards sees that it is gone.
func (owned *pidFile) release() error {
	same, err := stillAt(owned.file, owned.path)
	if err == nil && same {
		err = os.Remove(owned.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			err = fmt.Errorf("removing the pid file: %w", err)
		} else {
			err = nil
		}
	}

	return errors.Join(err, closePidFile(owned.file))
}

func closePidFile(file *os.File) error {
	err := file.Close()
	if err != nil {
		return fmt.Errorf("closing the pid file: %w", err)
	}

	return nil
}
