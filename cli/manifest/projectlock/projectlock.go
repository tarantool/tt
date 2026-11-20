// Package projectlock is the lock of a project that the commands which change
// it hold while they do: tt package build, fetch, pack, clean, add, remove,
// update and resolve, tt package install and uninstall in the project scope,
// tt test while it builds, and the methods of sdk.Project that write, so that
// two of them never change _build/, .rocks/, app.manifest.toml or
// app.manifest.lock of one project at once. What only reads a project - tt
// package deps and list, sdk.Project.Manifest and ReadLock - takes no lock:
// it would wait behind a build for minutes, so it may see the project
// between two steps of a command that changes it.
//
// The lock is an exclusive flock(2) on _build/tt.lock of the project. The
// build directory is where the build keeps its own state, which nothing
// hashes, packs or lays out; .tarantool/ is not: the content hash of a path
// dependency reads it, so a lock there would make the lock of every project
// that depends on the project stale. tt package clean keeps the file. The
// lock is that of the open file: the kernel drops it when its holder exits,
// however it exits, and a holder that crashed leaves a file nobody holds,
// which the next command takes at once. The descriptor is close-on-exec, so a
// program the holder runs, a daemon a build starts above all, does not keep
// the lock after tt exits.
//
// A command that finds the lock held waits for it, saying once whose it is,
// until its context is done: Ctrl-C stops the wait as it stops the command.
//
// The lock is reentrant in two ways, so that nothing deadlocks on itself:
//
//   - in the process, by the context: the context Acquire returns carries the
//     lock, and an Acquire with it, or with one derived from it, takes
//     nothing. A module command holding sdk.Project.Lock calls Build and
//     Resolve with that context. Another goroutine of the process, with
//     another context, waits as another process does;
//   - down to the programs the holder runs, by the environment: the holder
//     names the lock in TT_PROJECT_LOCK, which every program it runs inherits,
//     and a tt among them - one a hook or a shell backend runs on the same
//     project - works under the lock of the tt that runs it rather than wait
//     for it. A tt is taken for the holder's only while the holder holds the
//     lock: a value left in the environment of a shell after the holder exited
//     names no lock held, and is ignored. A program that a hook starts in the
//     background and that outlives the hook would work beside its tt.
//
// A project without app.manifest.toml gets no lock: no command that writes a
// project works there but tt package install and uninstall, which keep
// guests in .rocks/ of any directory, and a lock would create _build/ in it.
// A file system without flock(2) gets none either, with a warning.
package projectlock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/sdk/v3/log"
)

const (
	// FileName is the lock file in the build directory of a project.
	FileName = "tt.lock"
	// EnvVar names the locks a tt holds, for the tt processes it runs.
	EnvVar = "TT_PROJECT_LOCK"
	// BuildDirName is the build directory of a project, which the lock file
	// is in.
	BuildDirName = "_build"

	// manifestFileName marks a project that is locked.
	manifestFileName = "app.manifest.toml"
	// filePerm and dirPerm are the modes of the lock file and of the build
	// directory, when the lock creates them.
	filePerm = 0o644
	dirPerm  = 0o755
	// attempts bounds the retries when the lock file is replaced between
	// opening and locking it.
	attempts = 16
	// pollInterval is the pause between the attempts to take a lock held:
	// flock(2) that waits cannot be interrupted, the context can.
	pollInterval = 100 * time.Millisecond
	// unnamedPolls is how many polls a wait makes for a lock held whose file
	// names no holder before it says so. A tt writes its pid just after it
	// takes the lock and truncates the file just before it gives the lock
	// up: a waiter that reads the file in between finds no pid, and a poll
	// after finds the pid, or the lock free. A holder that names itself for
	// none of these polls, for a second, is not a tt.
	unnamedPolls = 10
	// maxSize is as much of the lock file as is read; a pid is shorter.
	maxSize = 64
)

// errReplaced reports a lock file replaced on every attempt to lock it.
var errReplaced = errors.New("the lock file keeps being replaced")

// Path returns the lock file of the project in dir.
func Path(dir string) string {
	return filepath.Join(dir, BuildDirName, FileName)
}

// Waiting is told whose lock an Acquire waits for: the pid of the tt that
// holds it, 0 when the file names no holder for unnamedPolls polls, and
// whether that is this process, another goroutine of it.
type Waiting func(pid int, self bool)

// Acquire takes the lock of the project in dir, waiting while another
// process, or another goroutine of this one, holds it, and saying once that
// it waits. It returns the context that carries the lock, for what is done
// under it, and the release, which may be called more than once. With a ctx
// that carries the lock already, or in a tt that the holder runs, it takes
// nothing; the release then releases nothing. A project without a manifest
// gets no lock. The error is that of the lock file, or of ctx done while it
// waits.
func Acquire(ctx context.Context, dir string) (context.Context, func(), error) {
	return acquire(ctx, dir, logWaiting(dir))
}

// logWaiting is the Waiting of Acquire: an info line, which tt shows.
func logWaiting(dir string) Waiting {
	return func(pid int, self bool) {
		switch {
		case self:
			log.Info("waiting for another operation of this tt on the project " + dir)
		case pid > 0:
			log.Info(fmt.Sprintf("waiting for another tt working on the project %s (pid %d)",
				dir, pid))
		default:
			log.Info("waiting for another tt working on the project " + dir)
		}
	}
}

// holdKey is the key of the context value that carries the lock of the file
// path.
type holdKey struct {
	path string
}

// Held reports whether ctx carries the lock of the project in dir.
func Held(ctx context.Context, dir string) bool {
	return ctx.Value(holdKey{path: Path(canonical(dir))}) != nil
}

// acquire is Acquire telling waiting whose lock it waits for.
func acquire(ctx context.Context, dir string, waiting Waiting) (context.Context, func(), error) {
	dir = canonical(dir)

	key := holdKey{path: Path(dir)}
	if ctx.Value(key) != nil {
		return ctx, func() {}, nil
	}

	//nolint:gosec // The project directory is the caller's.
	_, err := os.Stat(filepath.Join(dir, manifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return ctx, func() {}, nil
	}

	sem := semaphore(key.path)

	err = enter(ctx, sem, waiting)
	if err != nil {
		return ctx, func() {}, err
	}

	held, err := lockFile(ctx, key.path, waiting)
	if err != nil {
		<-sem

		return ctx, func() {}, err
	}

	var once sync.Once

	release := func() {
		once.Do(func() {
			held.release()
			<-sem
		})
	}

	return context.WithValue(ctx, key, true), release, nil
}

// canonical is dir absolute with its symbolic links resolved, so that two
// spellings of one project are one lock in the process; dir as it is when
// that fails.
func canonical(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}

	return resolved
}

// semaphores holds a semaphore of one for each lock file of the process: the
// lock between its goroutines, which flock(2) of one file opened twice would
// not be.
var semaphores = struct {
	sync.Mutex

	of map[string]chan struct{}
}{of: map[string]chan struct{}{}}

// semaphore returns the semaphore of the lock file path.
func semaphore(path string) chan struct{} {
	semaphores.Lock()
	defer semaphores.Unlock()

	sem, ok := semaphores.of[path]
	if !ok {
		sem = make(chan struct{}, 1)
		semaphores.of[path] = sem
	}

	return sem
}

// enter takes sem, waiting for it while ctx is not done.
func enter(ctx context.Context, sem chan struct{}, waiting Waiting) error {
	select {
	case sem <- struct{}{}:
		return nil
	default:
	}

	waiting(os.Getpid(), true)

	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for the project lock: %w", context.Cause(ctx))
	}
}

// hold is the lock file a process holds, or nothing: a lock it works under
// that is another's, or none on a file system without flock(2).
type hold struct {
	file  *os.File
	entry string
}

// release gives the lock up: the entry leaves the environment, the pid
// leaves the file, so that no tt takes the next holder for this one before
// the next holder writes its own, and closing the file unlocks it.
func (h hold) release() {
	if h.file == nil {
		return
	}

	unsetEntry(h.entry)

	_ = h.file.Truncate(0)
	_ = h.file.Close()
}

// lockFile takes the lock of the file path, creating it and its directory,
// or finds that this process works under it. A wait is told of the holder
// once, by the first poll that reads its pid, or with no pid once
// unnamedPolls polls have read none.
func lockFile(ctx context.Context, path string, waiting Waiting) (hold, error) {
	err := os.MkdirAll(filepath.Dir(path), dirPerm)
	if err != nil {
		return hold{}, fmt.Errorf("creating the directory of the project lock: %w", err)
	}

	told := false
	unnamed := 0

	for range attempts {
		held, retry, err := tryLock(ctx, path, func(pid int) {
			switch {
			case told:
			case pid <= 0 && unnamed < unnamedPolls:
				unnamed++
			default:
				told = true

				waiting(pid, false)
			}
		})
		if !retry {
			return held, err
		}
	}

	return hold{}, fmt.Errorf("%w: %s", errReplaced, path)
}

// tryLock makes one attempt to lock the file at path, waiting while it is
// held. It reports retry when the path was replaced after it was opened, so
// that the attempt is repeated on the file there now.
func tryLock(ctx context.Context, path string, wait func(pid int)) (hold, bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return hold{}, false, fmt.Errorf("opening the project lock: %w", err)
	}

	held, retry, err := lockOpen(ctx, file, path, wait)
	if held.file == nil {
		// Closing releases the lock, when it was taken.
		_ = file.Close()
	}

	return held, retry, err
}

func lockOpen(ctx context.Context, file *os.File, path string, wait func(pid int)) (
	hold, bool, error,
) {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)

		switch {
		case err == nil:
			return own(file, path)
		case unsupported(err):
			log.Warn(fmt.Sprintf("working on the project without its lock: %s cannot be "+
				"locked: %s", path, err))

			return hold{}, false, nil
		case !errors.Is(err, syscall.EWOULDBLOCK):
			return hold{}, false, fmt.Errorf("locking %s: %w", path, err)
		}

		holder := readPid(file)

		inherited, err := inheritedFrom(file, holder)
		if err != nil || inherited {
			return hold{}, false, err
		}

		wait(holder)

		select {
		case <-ctx.Done():
			return hold{}, false, fmt.Errorf("waiting for the project lock: %w",
				context.Cause(ctx))
		case <-time.After(pollInterval):
		}
	}
}

// own makes the lock taken on file this process's: the file is to be the
// one at path still - a file locked after the build directory was removed is
// not - and gets the pid, and the environment the entry of the lock.
func own(file *os.File, path string) (hold, bool, error) {
	same, err := stillAt(file, path)
	if err != nil || !same {
		return hold{}, !same && err == nil, err
	}

	err = file.Truncate(0)
	if err == nil {
		_, err = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}

	if err != nil {
		return hold{}, false, fmt.Errorf("writing %s: %w", path, err)
	}

	entry, err := entryOf(file, os.Getpid())
	if err != nil {
		return hold{}, false, err
	}

	setEntry(entry)

	return hold{file: file, entry: entry}, false, nil
}

// unsupported reports an error of flock(2) on a file system that has no
// locks.
func unsupported(err error) bool {
	return errors.Is(err, syscall.ENOLCK) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP)
}

// stillAt reports whether path names the open file.
func stillAt(file *os.File, path string) (bool, error) {
	opened, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("checking %s: %w", path, err)
	}

	named, err := os.Stat(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("checking %s: %w", path, err)
	}

	return os.SameFile(opened, named), nil
}

// readPid reads the pid of the holder in the lock file; 0 when it holds
// none.
func readPid(file *os.File) int {
	data, err := io.ReadAll(io.NewSectionReader(file, 0, maxSize))
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}

	return pid
}

// entryOf is the entry of the lock of file held by pid in EnvVar:
// pid:device:inode, which names the file however its path is spelt.
func entryOf(file *os.File, pid int) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("checking the project lock: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("checking the project lock: %w", errors.ErrUnsupported)
	}

	// Dev is an int32 on darwin and a uint64 on linux.
	dev := uint64(stat.Dev) //nolint:gosec,unconvert,nolintlint // A device number is not negative.

	return fmt.Sprintf("%d:%d:%d", pid, dev, stat.Ino), nil
}

// inheritedFrom reports whether this process works under the lock of file,
// held by holder: whether EnvVar, which this process inherited, names the
// lock of the file held by holder, another process.
func inheritedFrom(file *os.File, holder int) (bool, error) {
	if holder <= 0 || holder == os.Getpid() {
		return false, nil
	}

	entry, err := entryOf(file, holder)
	if err != nil {
		return false, err
	}

	return slices.Contains(entries(os.Getenv(EnvVar)), entry), nil
}

// environment serializes the changes of EnvVar.
var environment sync.Mutex

// entries are the entries of the value of EnvVar.
func entries(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' })
}

// setEntry adds entry to EnvVar of the process, for the programs it runs.
func setEntry(entry string) {
	environment.Lock()
	defer environment.Unlock()

	_ = os.Setenv(EnvVar, strings.Join(append(entries(os.Getenv(EnvVar)), entry), ","))
}

// unsetEntry removes entry from EnvVar of the process, and the variable
// when no entry is left.
func unsetEntry(entry string) {
	environment.Lock()
	defer environment.Unlock()

	left := slices.DeleteFunc(entries(os.Getenv(EnvVar)), func(one string) bool {
		return one == entry
	})

	if len(left) == 0 {
		_ = os.Unsetenv(EnvVar)

		return
	}

	_ = os.Setenv(EnvVar, strings.Join(left, ","))
}
