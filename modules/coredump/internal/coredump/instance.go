package coredump

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk"
)

var (
	// ErrNoCrash reports an instance whose watchdog recorded no crash.
	ErrNoCrash = errors.New("no crash is recorded")
	// ErrPackedByPipe reports the core of a crash the pipe of core_pattern
	// packed, whose archive is there and whose core is not.
	ErrPackedByPipe = errors.New("the core is packed already, by the kernel.core_pattern pipe")
)

// fetchedCoreMode is the mode of a core coredumpctl dumps for pack: the
// memory of a process is for its owner.
const fetchedCoreMode = 0o600

// InstanceCore is the core of the last crash of an instance that has one.
type InstanceCore struct {
	// Crash is the crash.
	Crash sdk.Crash
	// Core is where its core is.
	Core Core
	// Later are the crashes after it, the newest first, which dumped no
	// core.
	Later []sdk.Crash
}

// LastCore returns the core of the last crash of inst that dumped one, and
// the later crashes that dumped none. An instance whose crashes cannot be
// read is the error of why, an instance with no crash an error that wraps
// ErrNoCrash, one whose crashes dumped no core an error that wraps
// ErrNoCoreDumped, and a core that is not found the error of Locate, of the
// last crash that dumped one.
func (l *Locator) LastCore(ctx context.Context, inst sdk.LocalInstance) (InstanceCore, error) {
	var found InstanceCore

	if inst.CrashesErr != nil {
		return found, fmt.Errorf("%s: %w", inst.Name, inst.CrashesErr)
	}

	crashes := NewestFirst(inst.Crashes)
	if len(crashes) == 0 {
		return found, fmt.Errorf("%s: %w: its process has not ended by a signal since tt "+
			"records the crashes", inst.Name, ErrNoCrash)
	}

	for i, crash := range crashes {
		if !crash.CoreDumped {
			continue
		}

		found.Crash, found.Later = crash, crashes[:i]

		core, err := l.Locate(ctx, inst, crash)
		if err != nil {
			return found, fmt.Errorf("the core of the crash of %s, %s: %w", inst.Name,
				DescribeCrash(crash), err)
		}

		found.Core = core

		if core.Source == SourceArchive {
			return found, fmt.Errorf("the crash of %s, %s: %w: %s", inst.Name,
				DescribeCrash(crash), ErrPackedByPipe, core.Path)
		}

		return found, nil
	}

	return found, fmt.Errorf("%s: %w by any of its %d crashes: the last, %s; the core size "+
		"limit of its process, ulimit -c, was 0, or the kernel could not write it where "+
		"kernel.core_pattern says", inst.Name, ErrNoCoreDumped, len(crashes),
		DescribeCrash(crashes[0]))
}

// DescribeCrash tells of crash: its PID, the signal and when, as the
// watchdog recorded it.
func DescribeCrash(crash sdk.Crash) string {
	return fmt.Sprintf("PID %d, %s, at %s", crash.PID, SignalName(crash.Signal),
		crash.At.Format(time.DateTime))
}

// DescribeSource tells where core came from, for pack to say so.
func DescribeSource(core Core) string {
	switch core.Source {
	case SourceCoredumpctl:
		return "systemd-coredump, dumped by coredumpctl"
	case SourcePipe:
		return "the core the kernel.core_pattern pipe stored, " + core.Path
	case SourceApport:
		return "the core apport wrote, " + core.Path
	default:
		return "the file of kernel.core_pattern, " + core.Path
	}
}

// Fetch returns the file of the core of found to pack, and the function
// that removes it when it is a file of its own: the core systemd-coredump
// keeps is dumped by coredumpctl into a new file of dir, the directory the
// archive goes into, through its standard output, the blocks of zeros left
// holes (see writeSparse), so that the file takes the space of the data of
// the core alone, as the core of a pipe does. Any other core is its file,
// which stays.
//
// The core dumped is the one of the entry of the crash: coredumpctl dump
// takes the last entry its matches select, whatever --since and --until
// say, so the entry is pinned by its fields, COREDUMP_PID and
// COREDUMP_TIMESTAMP, the time of the dump (see coredumpTimestamp). A core
// of another process than the crash, by the PID of its NT_PRPSINFO, is
// removed and refused (ErrOtherProcess). A ctx that ends, as a signal to tt
// ends it, ends coredumpctl, and the file is removed.
func (l *Locator) Fetch(ctx context.Context, found InstanceCore, dir string) (
	string, func(), error,
) {
	if found.Core.Source != SourceCoredumpctl {
		return found.Core.Path, func() {}, nil
	}

	if dir == "" {
		dir = "."
	}

	pid := found.Crash.PID

	timestamp, err := l.coredumpTimestamp(ctx, pid, found.Core.Time)
	if err != nil {
		return "", nil, err
	}

	file, err := os.CreateTemp(dir, ".tt-coredump-"+strconv.Itoa(pid)+"-*.core")
	if err != nil {
		return "", nil, fmt.Errorf("cannot create a file for the core coredumpctl dumps: %w", err)
	}

	path := file.Name()
	remove := func() { _ = os.Remove(path) }

	err = file.Chmod(fetchedCoreMode)
	if err == nil {
		err = l.dumpCore(ctx, file, pid, timestamp)
	}

	err = errors.Join(err, file.Close())
	if err != nil {
		remove()

		return "", nil, fmt.Errorf("cannot dump the core of the PID %d with %s: %w", pid,
			coredumpctlName, err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		remove()

		return "", nil, fmt.Errorf("%w: %s dump of the PID %d wrote no core into %s",
			ErrCoreNotFound, coredumpctlName, pid, path)
	}

	dumped, told := corePID(path)
	if told && dumped != pid {
		remove()

		return "", nil, fmt.Errorf("%w: %s dumped the core of the PID %d, not of the PID %d of "+
			"the crash", ErrOtherProcess, coredumpctlName, dumped, pid)
	}

	return path, remove, nil
}

// coredumpTimestamp returns COREDUMP_TIMESTAMP of the entry of systemd-coredump
// of the PID pid that coredumpctl lists at the time listed, to the microsecond,
// as locateCoredumpctl found it: the time of the dump in microseconds, which
// pins the entry for coredumpctl dump. coredumpctl list honours --since and
// --until, and those of the time select the entry alone.
func (l *Locator) coredumpTimestamp(ctx context.Context, pid int, listed time.Time) (
	string, error,
) {
	when := fmt.Sprintf("@%d.%06d", listed.Unix(), listed.Nanosecond()/int(time.Microsecond))

	var out bytes.Buffer

	err := l.coredumpctl(ctx, &out, "--no-pager", "--field=COREDUMP_TIMESTAMP", "--since="+when,
		"--until="+when, "list", strconv.Itoa(pid))
	if err != nil {
		return "", fmt.Errorf("cannot tell the core of the crash from the other cores of the PID "+
			"%d %s keeps: %w", pid, coredumpctlName, err)
	}

	values := slices.Compact(strings.Fields(out.String()))
	if len(values) != 1 || strings.Trim(values[0], "0123456789") != "" {
		return "", fmt.Errorf("%w: cannot tell the core of the crash from the other cores of "+
			"the PID %d %s keeps: it lists %q as COREDUMP_TIMESTAMP of the entry of %s",
			ErrCoreNotFound, pid, coredumpctlName, values, listed.Format(time.DateTime))
	}

	return values[0], nil
}

// dumpCore writes the core of the entry of systemd-coredump of the PID pid
// and of COREDUMP_TIMESTAMP timestamp into file, the standard output of
// coredumpctl dump written by writeSparse.
func (l *Locator) dumpCore(ctx context.Context, file *os.File, pid int, timestamp string) error {
	reader, writer := io.Pipe()
	ran := make(chan error, 1)

	go func() {
		err := l.coredumpctl(ctx, writer, "--no-pager", "dump",
			"COREDUMP_PID="+strconv.Itoa(pid), "COREDUMP_TIMESTAMP="+timestamp)

		_ = writer.CloseWithError(err)

		ran <- err
	}()

	err := writeSparse(file, reader)

	// A write that fails leaves coredumpctl a pipe no one reads.
	_ = reader.Close()

	runErr := <-ran

	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("%w", context.Cause(ctx))
	case runErr != nil:
		return runErr
	default:
		return err
	}
}

// IsInstanceTarget reports whether arg, the COREDUMP of pack, names an
// instance rather than a core file: it is no file, and it is
// <app>:<instance> or <app>:*.
func IsInstanceTarget(arg string) bool {
	_, err := os.Lstat(arg)
	if err == nil {
		return false
	}

	app, inst, found := strings.Cut(arg, ":")

	return found && app != "" && inst != "" && !strings.ContainsAny(arg, "/,")
}
