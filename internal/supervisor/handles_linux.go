package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// errNoSignalMasks is the error of a status file that shows no signal masks.
var errNoSignalMasks = errors.New("no signal masks")

// signalMasks are the signal masks /proc/<pid>/status shows: the dispositions
// of the process and the mask of the thread the file belongs to.
type signalMasks struct {
	blocked, ignored, caught uint64
}

// handlesSignal tells whether the process pid survives sig: it catches the
// signal, ignores it, or blocks it in every thread, as a program that reads
// its signals from a signalfd does. A program that has only just been
// started does none of that until it sets up its signal handling and dies of
// a signal whose default action ends it. It reports true when it cannot
// tell, as for a signal out of the range of the masks.
func handlesSignal(pid int, sig syscall.Signal) bool {
	if sig < 1 || sig > 64 {
		return true
	}

	bit := uint64(1) << (sig - 1)
	dir := filepath.Join("/proc", strconv.Itoa(pid))

	process, err := readSignalMasks(filepath.Join(dir, "status"))
	if err != nil {
		return true
	}

	if (process.ignored|process.caught)&bit != 0 {
		return true
	}

	tasks, err := os.ReadDir(filepath.Join(dir, "task"))
	if err != nil || len(tasks) == 0 {
		return true
	}

	for _, task := range tasks {
		thread, err := readSignalMasks(filepath.Join(dir, "task", task.Name(), "status"))

		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ESRCH):
			// The thread has exited since the directory was read.
			continue
		case err != nil:
			return true
		case thread.blocked&bit == 0:
			return false
		}
	}

	return true
}

// readSignalMasks reads the signal masks from path, a status file of /proc.
func readSignalMasks(path string) (signalMasks, error) {
	var masks signalMasks

	data, err := os.ReadFile(path)
	if err != nil {
		return masks, fmt.Errorf("reading %s: %w", path, err)
	}

	fields := map[string]*uint64{
		"SigBlk": &masks.blocked,
		"SigIgn": &masks.ignored,
		"SigCgt": &masks.caught,
	}
	found := 0

	for line := range strings.Lines(string(data)) {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		field, ok := fields[name]
		if !ok {
			continue
		}

		*field, err = strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return masks, fmt.Errorf("%s: %s: %w", path, name, err)
		}

		found++
	}

	if found != len(fields) {
		return masks, fmt.Errorf("%s: %w", path, errNoSignalMasks)
	}

	return masks, nil
}
