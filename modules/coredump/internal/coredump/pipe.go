package coredump

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// StdinCore is the COREDUMP of pack that names its standard input, where a
// kernel.core_pattern pipe writes the core.
const StdinCore = "-"

// tarantoolComm is the comm of a Tarantool process, the name of its
// executable: the core of a process of another comm is stored, not packed.
const tarantoolComm = "tarantool"

// procDir is the mount point of procfs.
const procDir = "/proc"

// coreFileMode is the mode of the file a core of the standard input is
// stored to: the memory of a process is for its owner, as Linux writes a
// core.
const coreFileMode = 0o600

// sparseBlock is the size of the blocks of a core of the standard input
// that are left holes of the file it is stored to when they are all zeros.
const sparseBlock = 64 << 10

var (
	// errNoProcess reports a core of the standard input whose process
	// procfs does not have.
	errNoProcess = errors.New("no process of the core on the standard input")
	// errNoStdin reports a core of the standard input where there is none.
	errNoStdin = errors.New("no standard input to read the core from")
)

// ReceiveCore takes the core of the process opts.PID from stdin, the
// standard input, as the kernel.core_pattern pipe `|tt coredump pack -d DIR -p %p -t
// %t` gives it, when opts.Core is empty or StdinCore, and returns opts to
// pack it with. procfs has the process while the pipe runs: its executable
// is /proc/PID/exe, unless opts.Executable names one, and its comm is
// /proc/PID/comm. The core is stored to <DIR>/<comm>-core.<PID>.<TIME>, with
// the Time of opts, now when it has none, and opts.Core names it. The core
// of a process whose comm is not tarantool is stored alone: ReceiveCore
// returns false, for nothing to be packed, as the pack script did.
//
// opts with a core of a file come back as they are, with true: the PID
// names the archive alone, as a process of the PID in procfs, if any, is
// another one by now. A core of the standard input whose process procfs
// does not have is an error.
func ReceiveCore(stdin io.Reader, opts PackOptions) (PackOptions, bool, error) {
	return receiveCore(stdin, opts, procDir)
}

// receiveCore is ReceiveCore with procfs mounted at proc.
func receiveCore(stdin io.Reader, opts PackOptions, proc string) (PackOptions, bool, error) {
	if opts.Core != "" && opts.Core != StdinCore {
		return opts, true, nil
	}

	pid := strconv.FormatUint(uint64(opts.PID), 10)
	dir := filepath.Join(proc, pid)

	info, err := os.Stat(dir)
	if opts.PID == 0 || err != nil || !info.IsDir() {
		return opts, false, fmt.Errorf("%w: no %s: the core on the standard input is that of "+
			"a process of procfs, as a kernel.core_pattern pipe gives it; give COREDUMP as a "+
			"file otherwise", errNoProcess, dir)
	}

	// The process is read before the core: once the core is written,
	// Linux may reap it.
	comm, err := os.ReadFile(filepath.Join(dir, "comm"))
	if err != nil {
		return opts, false, fmt.Errorf("cannot read the comm of the process %s: %w", pid, err)
	}

	name := strings.TrimSuffix(string(comm), "\n")

	if opts.Executable == "" {
		opts.Executable = processExecutable(dir)
	}

	if opts.Time == "" {
		opts.Time = strconv.FormatInt(time.Now().Unix(), 10)
	}

	// A comm with a slash would name a file in another directory.
	opts.Core = filepath.Join(opts.Dir, fmt.Sprintf("%s-core.%s.%s",
		strings.ReplaceAll(name, "/", "_"), pid, opts.Time))

	err = storeCore(opts.Core, stdin)
	if err != nil {
		return opts, false, err
	}

	return opts, name == tarantoolComm, nil
}

// processExecutable returns the executable of the process of the procfs
// directory dir: the path its exe links to, or the link itself when no
// such path is the file the process runs, as when the file was deleted
// since it was run, which the path then ends with " (deleted)" for, or the
// path is of another mount namespace.
func processExecutable(dir string) string {
	exe := filepath.Join(dir, "exe")

	target, err := os.Readlink(exe)
	if err != nil {
		return exe
	}

	running, err := os.Stat(exe)
	if err != nil {
		return exe
	}

	info, err := os.Stat(target)
	if err != nil || !os.SameFile(running, info) {
		return exe
	}

	return target
}

// storeCore writes the core of stdin to a new file at path. The file is
// removed when the core cannot be written whole.
//
//nolint:gosec // G703: path is in the directory the user named, by a comm with no slash.
func storeCore(path string, stdin io.Reader) error {
	if stdin == nil {
		return errNoStdin
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, coreFileMode)
	if err != nil {
		return fmt.Errorf("cannot store the core on the standard input: %w", err)
	}

	err = writeSparse(file, stdin)

	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		_ = os.Remove(path)

		return fmt.Errorf("cannot store the core on the standard input to %s: %w", path, err)
	}

	return nil
}

// writeSparse copies stdin to file, the blocks of zeros left holes. Linux
// leaves the pages a process never touched holes of a core it writes to a
// file, and writes them as zeros to a pipe: those of a bare tarantool make
// most of its gigabyte.
func writeSparse(file *os.File, stdin io.Reader) error {
	block := make([]byte, sparseBlock)
	zeros := make([]byte, sparseBlock)

	var size int64

	for {
		read, err := io.ReadFull(stdin, block)
		if read > 0 {
			var writeErr error

			if bytes.Equal(block[:read], zeros[:read]) {
				_, writeErr = file.Seek(int64(read), io.SeekCurrent)
			} else {
				_, writeErr = file.Write(block[:read])
			}

			if writeErr != nil {
				return fmt.Errorf("cannot write the core: %w", writeErr)
			}

			size += int64(read)
		}

		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}

		if err != nil {
			return fmt.Errorf("cannot read the core: %w", err)
		}
	}

	// A core that ends with zeros ends with a hole, which only the size of
	// the file makes.
	err := file.Truncate(size)
	if err != nil {
		return fmt.Errorf("cannot write the core: %w", err)
	}

	return nil
}
