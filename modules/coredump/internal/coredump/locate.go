package coredump

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk"
)

const (
	// corePatternFile and coreUsesPIDFile are the files of procfs that hold
	// kernel.core_pattern and kernel.core_uses_pid.
	corePatternFile = "sys/kernel/core_pattern"
	coreUsesPIDFile = "sys/kernel/core_uses_pid"
	// coreWindow is how long before the watchdog saw the process end its
	// core may have been dumped: the kernel writes the core of a process
	// before it reaps it, and a core of gigabytes takes its time. A program
	// of a pipe may write its files as long after it.
	coreWindow = 10 * time.Minute
	// coreSlack is how long after the watchdog saw the process end the core
	// the kernel wrote into a file may seem written: the clocks of the file
	// system and of the watchdog tick apart.
	coreSlack = 2 * time.Second
	// apportCrashDir is where apport writes the reports of crashes.
	apportCrashDir = "/var/crash"
	// apportCoreDir is where apport writes the cores it keeps, core_dir of
	// apport.fileutils: those of a process of an executable of no package,
	// which it reports none of, and of SIGQUIT.
	apportCoreDir = "/var/lib/apport/coredump"
	// bootIDFile is the file of procfs with the ID of the boot, which names
	// a core apport keeps.
	bootIDFile = "sys/kernel/random/boot_id"
	// coredumpctlName is the program of systemd that lists and dumps the
	// cores of systemd-coredump.
	coredumpctlName = "coredumpctl"
	// archiveTimeLayout is the time in the name of an archive of pack,
	// date +%Y%m%d%H%M of the pack script, in the local time of the host.
	archiveTimeLayout = "200601021504"
)

// The sources of a core: where it was found.
const (
	// SourceFile is a core file a pattern of a file names.
	SourceFile = "file"
	// SourceCoredumpctl is a core systemd-coredump keeps, which coredumpctl
	// dumps.
	SourceCoredumpctl = "coredumpctl"
	// SourcePipe is a core tt coredump pack, the pipe of core_pattern, stored
	// next to the archive it packed of it.
	SourcePipe = "tt-pipe"
	// SourceArchive is an archive tt coredump pack, the pipe of
	// core_pattern, packed, whose core is not there any more.
	SourceArchive = "tt-archive"
	// SourceApport is a core apport kept: in /var/lib/apport/coredump, or,
	// of an older apport, in the working directory of the process.
	SourceApport = "apport"
)

var (
	// ErrNoCoreDumped reports a crash the kernel dumped no core of.
	ErrNoCoreDumped = errors.New("no core was dumped")
	// ErrCoreNotFound reports a core that is not where core_pattern says.
	ErrCoreNotFound = errors.New("the core is not found")
	// ErrOtherProcess reports a core where the one of a crash is to be that
	// a process of another PID dumped, by its NT_PRPSINFO note: of another
	// instance that crashed then, with a pattern that does not name the
	// PID. It wraps ErrCoreNotFound.
	ErrOtherProcess = fmt.Errorf("%w: it is the core of another process", ErrCoreNotFound)
	// errUnknownPipe reports a core_pattern of a pipe to a program tt does
	// not know.
	errUnknownPipe = errors.New("kernel.core_pattern pipes the cores to a program tt does " +
		"not know")
)

// Core is where the core of a crash is.
type Core struct {
	// Source is how it was found: SourceFile, SourceCoredumpctl,
	// SourcePipe, SourceArchive or SourceApport.
	Source string
	// Path is the core file, or the archive of a SourceArchive; empty for a
	// core coredumpctl keeps, which it dumps by its PID.
	Path string
	// Size is the size of the file in bytes, or the size coredumpctl tells,
	// 0 when it tells none.
	Size int64
	// Archive is the archive the pipe packed of a SourcePipe core, empty
	// when there is none.
	Archive string
	// Time is when the core was written: the modification time of the file,
	// the time of the dump in its name, or the time coredumpctl tells.
	Time time.Time
}

// corePattern is kernel.core_pattern as the kernel has it now.
type corePattern struct {
	// pattern is the pattern itself.
	pattern string
	// usesPID is kernel.core_uses_pid.
	usesPID bool
}

// Locator finds the core of a crash of an instance where kernel.core_pattern
// puts it, as the pattern is now, read once: a crash under another pattern
// is looked for under this one.
//
//   - A pattern of a file is expanded as core(5) expands it, with what a crash
//     of an instance tells: %p and %P, the PID, %s, the signal, %h, the
//     host name, and %%; the other specifiers, which name what the crash does
//     not tell (the time, the comm, the executable, the IDs, the limits),
//     match anything. core_uses_pid appends .PID to a pattern without %p,
//     and a relative pattern is relative to the working directory of the
//     process. The core is the file written last before the watchdog saw
//     the process end, within coreWindow.
//   - |/…/systemd-coredump has the cores of systemd-coredump, which
//     coredumpctl lists by their PID and time, and dumps.
//   - |/…/tt coredump pack -d DIR … is tt as the pipe: DIR has the core it
//     stored, <comm>-core.<PID>.<TIME>, and the archive it packed,
//     tarantool-core-<PID>-<TIME>-<HOST>.tar.gz, of the time of the dump.
//   - |/…/apport keeps the core of an executable of no package, and of a
//     SIGQUIT, in /var/lib/apport/coredump, as
//     core.<executable>.<UID>.<boot ID>.<PID>.<start time>, the executable
//     its path with / and . made _, and writes a report of the crash into
//     /var/crash otherwise, which apport-unpack unpacks. An older apport
//     wrote the core into the working directory of the process, as the
//     kernel writes core, and is looked for there too.
//   - Any other pipe is a program tt does not know: where its cores go is
//     its own.
//
// A core file found is checked against the crash by the PID of its
// NT_PRPSINFO note: one of another process, as of another instance of the
// application that crashed then into the core of a pattern with no %p, is
// not taken (ErrOtherProcess).
type Locator struct {
	// pattern is kernel.core_pattern; patternErr is why it cannot be read.
	pattern    corePattern
	patternErr error
	// hostname is the host name, %h.
	hostname string
	// crashDir is the directory of the reports of apport, and apportDir the
	// directory of the cores it keeps.
	crashDir, apportDir string
	// bootID is the ID of the boot of the host, which names a core apport
	// keeps; empty when it cannot be read, which matches any.
	bootID string
	// coredumpctl runs coredumpctl with the arguments, its standard output
	// written to stdout.
	coredumpctl func(ctx context.Context, stdout io.Writer, args ...string) error
}

// NewLocator returns the Locator of the cores of the crashes of this host.
func NewLocator() *Locator {
	return newLocator(procDir)
}

// newLocator returns a Locator that reads kernel.core_pattern and the ID of
// the boot from procfs mounted at proc.
func newLocator(proc string) *Locator {
	pattern, err := readCorePattern(proc)

	hostname, hostErr := os.Hostname()
	if hostErr != nil {
		hostname = "*"
	}

	bootID, _ := os.ReadFile(filepath.Join(proc, bootIDFile))

	return &Locator{
		pattern: pattern, patternErr: err, hostname: hostname, crashDir: apportCrashDir,
		apportDir: apportCoreDir, bootID: strings.TrimSpace(string(bootID)),
		coredumpctl: runCoredumpctl,
	}
}

// Pattern returns kernel.core_pattern as the Locator read it, or why it
// cannot be read.
func (l *Locator) Pattern() (string, error) {
	return l.pattern.pattern, l.patternErr
}

// readCorePattern reads kernel.core_pattern and kernel.core_uses_pid from
// procfs mounted at proc.
func readCorePattern(proc string) (corePattern, error) {
	data, err := os.ReadFile(filepath.Join(proc, corePatternFile))
	if err != nil {
		return corePattern{pattern: "", usesPID: false},
			fmt.Errorf("cannot read kernel.core_pattern: %w", err)
	}

	usesPID := false

	uses, err := os.ReadFile(filepath.Join(proc, coreUsesPIDFile))
	if err == nil {
		usesPID = strings.TrimSpace(string(uses)) != "0"
	}

	return corePattern{pattern: strings.TrimSuffix(string(data), "\n"), usesPID: usesPID}, nil
}

// Locate returns where the core of crash of inst is, or an error that says
// why none can be found: the crash dumped no core, which wraps
// ErrNoCoreDumped; the pattern cannot be read, or is of a pipe to a program
// tt does not know; or the core is not where the pattern says, which wraps
// ErrCoreNotFound.
func (l *Locator) Locate(ctx context.Context, inst sdk.LocalInstance, crash sdk.Crash) (
	Core, error,
) {
	var none Core

	if !crash.CoreDumped {
		return none, fmt.Errorf("%w: the core size limit of the process, ulimit -c, was 0, "+
			"or the kernel could not write it where kernel.core_pattern says", ErrNoCoreDumped)
	}

	if l.patternErr != nil {
		return none, l.patternErr
	}

	program, args, piped := pipeOf(l.pattern.pattern)
	if !piped {
		return l.locateFile(inst, crash)
	}

	switch {
	case filepath.Base(program) == "systemd-coredump":
		return l.locateCoredumpctl(ctx, crash)
	case isPackPipe(args):
		return locatePipe(crash, args)
	case filepath.Base(program) == "apport":
		return l.locateApport(inst, crash)
	default:
		return none, fmt.Errorf("%w, %s: look for the core where it keeps them, by the PID %d",
			errUnknownPipe, program, crash.PID)
	}
}

// pipeOf returns the program and the arguments of pattern, a pipe, and
// whether it is one: the kernel splits a pipe at spaces and quotes nothing.
func pipeOf(pattern string) (string, []string, bool) {
	rest, piped := strings.CutPrefix(pattern, "|")
	if !piped {
		return "", nil, false
	}

	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", nil, true
	}

	return fields[0], fields[1:], true
}

// isPackPipe reports whether args are those of tt coredump pack.
func isPackPipe(args []string) bool {
	for i := range len(args) - 1 {
		if args[i] == "coredump" && args[i+1] == "pack" {
			return true
		}
	}

	return false
}

// locateFile finds the core of crash of inst that a pattern of a file names
// (expandPattern): the file written last within coreWindow before the
// watchdog saw the process end.
func (l *Locator) locateFile(inst sdk.LocalInstance, crash sdk.Crash) (Core, error) {
	glob := expandPattern(l.pattern, crash, l.hostname)
	if !filepath.IsAbs(glob) {
		glob = filepath.Join(escapeGlob(inst.WorkDir), glob)
	}

	return locateGlob(SourceFile, glob, crash, coreSlack)
}

// locateGlob returns the core of crash among the files glob matches, by
// their modification time: the one written last within coreWindow before
// the watchdog saw the process end, or after up to after, of the PID of the
// crash. None is an error that names glob, the file of another time that
// matches it, or the core of another process of the time (ErrOtherProcess).
func locateGlob(source, glob string, crash sdk.Crash, after time.Duration) (Core, error) {
	near, err := newestNear(glob, crash, after, modTime, true)
	if err != nil {
		return near.found, err
	}

	if near.found.Path != "" {
		near.found.Source = source

		return near.found, nil
	}

	err = near.foreignErr(crash)
	if err != nil {
		return near.found, err
	}

	if near.other.Path != "" {
		return near.found, fmt.Errorf("%w: %s is of another crash, written at %s",
			ErrCoreNotFound, near.other.Path, near.other.Time.Format(time.DateTime))
	}

	return near.found, fmt.Errorf("%w: no file matches %s: it was removed or moved, or the "+
		"kernel could not write it there", ErrCoreNotFound, glob)
}

// timeOf returns when the core of the file at path, of info, was written,
// and whether it tells.
type timeOf func(path string, info os.FileInfo) (time.Time, bool)

// modTime is the timeOf a file by its modification time.
func modTime(_ string, info os.FileInfo) (time.Time, bool) {
	return info.ModTime(), true
}

// nearFiles are the files newestNear finds.
type nearFiles struct {
	// found is the file of the crash, other one of another time, and
	// foreign the core of the time of another process, of the PID
	// foreignPID; each has no Path when there is none.
	found, other, foreign Core
	foreignPID            int
}

// foreignErr returns the error of near that found no core of crash but the
// core of another process, nil otherwise.
func (near nearFiles) foreignErr(crash sdk.Crash) error {
	if near.foreign.Path == "" {
		return nil
	}

	return fmt.Errorf("%w: %s, written at %s, is the core of the PID %d, not of the PID %d of "+
		"the crash", ErrOtherProcess, near.foreign.Path,
		near.foreign.Time.Format(time.DateTime), near.foreignPID, crash.PID)
}

// newestNear returns the regular file glob matches whose time, as when
// tells it, is the latest within coreWindow before crash, or after up to
// after, and, when there is none, one of another time. With byPID, a core
// of another process than that of crash, by the PID of its NT_PRPSINFO
// note, is passed over, and returned as foreign; a file that tells no PID,
// as one that is no core, is taken.
func newestNear(glob string, crash sdk.Crash, after time.Duration, when timeOf, byPID bool) (
	nearFiles, error,
) {
	paths, err := filepath.Glob(glob)
	if err != nil {
		return nearFiles{}, fmt.Errorf("cannot match %s: %w", glob, err)
	}

	return newestAmong(paths, crash, after, when, byPID), nil
}

// newestAmong is newestNear of the files at paths.
func newestAmong(paths []string, crash sdk.Crash, after time.Duration, when timeOf,
	byPID bool,
) nearFiles {
	var near nearFiles

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		written, ok := when(path, info)
		if !ok {
			continue
		}

		core := Core{Source: "", Path: path, Size: info.Size(), Archive: "", Time: written}

		if !withinWindow(written, crash.At, after) {
			near.other = core

			continue
		}

		if byPID {
			pid, told := corePID(path)
			if told && pid != crash.PID {
				near.foreign, near.foreignPID = core, pid

				continue
			}
		}

		if near.found.Path == "" || written.After(near.found.Time) {
			near.found = core
		}
	}

	return near
}

// corePID returns the PID of the process of the core file at path, by its
// NT_PRPSINFO note, and whether it tells one: a file that is no core, or a
// core with no PID, tells none.
func corePID(path string) (int, bool) {
	core, err := openCore(path)
	if err != nil {
		return 0, false
	}

	defer func() { _ = core.Close() }()

	pid, ok := core.pid()

	return int(pid), ok
}

// withinWindow reports whether a core written at written may be of a crash
// the watchdog saw at at: within coreWindow before it, or after up to after.
func withinWindow(written, at time.Time, after time.Duration) bool {
	return !written.Before(at.Add(-coreWindow)) && !written.After(at.Add(after))
}

// expandPattern returns the glob of the files the pattern of a file names
// for crash, on the host hostname, as core(5) expands it: %% is %, %p and
// %P the PID, %s the signal, %h the host name, and every other specifier,
// which crash does not tell, any part of a name; an unknown one, and a
// trailing %, are dropped, as the kernel drops them. core_uses_pid appends
// .PID to a pattern without %p.
func expandPattern(pattern corePattern, crash sdk.Crash, hostname string) string {
	var glob strings.Builder

	pid := strconv.Itoa(crash.PID)
	hasPID := false
	// specifier tells that the character before was a % that starts one.
	specifier := false

	for _, char := range pattern.pattern {
		if !specifier {
			specifier = char == '%'
			if !specifier {
				glob.WriteString(escapeGlob(string(char)))
			}

			continue
		}

		specifier = false

		switch char {
		case '%':
			glob.WriteString("%")
		case 'p':
			hasPID = true

			glob.WriteString(pid)
		case 'P':
			glob.WriteString(pid)
		case 's':
			glob.WriteString(strconv.Itoa(int(crash.Signal)))
		case 'h':
			glob.WriteString(escapeGlob(hostname))
		case 'c', 'd', 'e', 'E', 'f', 'g', 'i', 'I', 't', 'u':
			glob.WriteString("*")
		}
	}

	if pattern.usesPID && !hasPID {
		glob.WriteString("." + pid)
	}

	return glob.String()
}

// escapeGlob escapes the characters of name filepath.Match takes for a
// pattern.
func escapeGlob(name string) string {
	var escaped strings.Builder

	for _, char := range name {
		if strings.ContainsRune(`*?[\`, char) {
			escaped.WriteRune('\\')
		}

		escaped.WriteRune(char)
	}

	return escaped.String()
}

// coredumpctlEntry is a core systemd-coredump keeps, as coredumpctl
// --json=short list prints it.
type coredumpctlEntry struct {
	// Time is when the process dumped it, in microseconds since the Epoch.
	Time int64 `json:"time"`
	// PID is the PID of the process.
	PID int `json:"pid"`
	// Corefile tells whether the core is kept: "present", "journal", or
	// why not, "missing", "none", "inaccessible", "error" or "truncated".
	Corefile string `json:"corefile"`
	// Size is the size of the core, when coredumpctl tells it.
	Size int64 `json:"size"`
}

// runCoredumpctl runs coredumpctl with args, its standard output written to
// stdout; an error has what it said on its standard error.
func runCoredumpctl(ctx context.Context, stdout io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, coredumpctlName, args...)

	var stderr strings.Builder

	cmd.Stdout = stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}

		return fmt.Errorf("%w", err)
	}

	return nil
}

// locateCoredumpctl finds the core of crash systemd-coredump keeps: the
// entry coredumpctl lists of its PID nearest the time of the crash, within
// coreWindow either way. The Time of the core is that of the entry, to the
// microsecond, which tells it from another of the PID.
func (l *Locator) locateCoredumpctl(ctx context.Context, crash sdk.Crash) (Core, error) {
	var none Core

	pid := strconv.Itoa(crash.PID)

	var listed bytes.Buffer

	err := l.coredumpctl(ctx, &listed, "--no-pager", "--json=short", "list", pid)
	out := listed.Bytes()

	var exitErr *exec.ExitError

	switch {
	case errors.Is(err, exec.ErrNotFound):
		return none, fmt.Errorf("kernel.core_pattern pipes the cores to systemd-coredump, "+
			"and %s is not found: %w", coredumpctlName, err)
	case errors.As(err, &exitErr) && strings.Contains(err.Error(), "No coredumps found"):
		return none, fmt.Errorf("%w: %s has no core of the PID %d", ErrCoreNotFound,
			coredumpctlName, crash.PID)
	case err != nil:
		return none, fmt.Errorf("cannot list the cores of the PID %d with %s: %w", crash.PID,
			coredumpctlName, err)
	}

	var entries []coredumpctlEntry

	err = json.Unmarshal(out, &entries)
	if err != nil {
		return none, fmt.Errorf("cannot read what %s --json=short list %s prints: %w",
			coredumpctlName, pid, err)
	}

	entry, ok := nearestEntry(entries, crash)
	if !ok {
		return none, fmt.Errorf("%w: %s has no core of the PID %d of %s", ErrCoreNotFound,
			coredumpctlName, crash.PID, crash.At.Format(time.DateTime))
	}

	if entry.Corefile != "present" && entry.Corefile != "journal" {
		return none, fmt.Errorf("%w: %s has the crash of the PID %d, and its core is %s",
			ErrCoreNotFound, coredumpctlName, crash.PID, entry.Corefile)
	}

	return Core{
		Source: SourceCoredumpctl, Path: "", Size: entry.Size, Archive: "",
		Time: time.UnixMicro(entry.Time),
	}, nil
}

// nearestEntry returns the entry of the PID of crash dumped nearest the
// time of the crash, within coreWindow of it either way, and whether there
// is one: systemd-coredump logs the entry once it has read the core, about
// when the watchdog sees the process end. The latest of the window was the
// core of a later process of the PID, as Linux reuses it, of the minutes
// after the crash.
func nearestEntry(entries []coredumpctlEntry, crash sdk.Crash) (coredumpctlEntry, bool) {
	var (
		nearest  coredumpctlEntry
		distance time.Duration
		found    bool
	)

	for _, entry := range entries {
		dumped := time.UnixMicro(entry.Time)
		if entry.PID != crash.PID || !withinWindow(dumped, crash.At, coreWindow) {
			continue
		}

		apart := dumped.Sub(crash.At).Abs()
		if !found || apart < distance {
			nearest, distance, found = entry, apart, true
		}
	}

	return nearest, found
}

// packDir returns the directory of the archives of the pipe of tt coredump
// pack whose arguments are args: -d or --dir, relative to the root, the
// working directory of a pipe, or the root itself.
func packDir(args []string) string {
	for i, arg := range args {
		switch {
		case (arg == "-d" || arg == "--dir") && i+1 < len(args):
			return absoluteIn("/", args[i+1])
		case strings.HasPrefix(arg, "--dir="):
			return absoluteIn("/", strings.TrimPrefix(arg, "--dir="))
		case strings.HasPrefix(arg, "-d") && len(arg) > len("-d"):
			return absoluteIn("/", strings.TrimPrefix(arg, "-d"))
		}
	}

	return "/"
}

// absoluteIn returns path, made absolute against dir when it is relative.
func absoluteIn(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(dir, path)
}

// storedTime is the timeOf a core the pipe stored, <comm>-core.<PID>.<TIME>:
// TIME, %t, in seconds since the Epoch.
func storedTime(path string, _ os.FileInfo) (time.Time, bool) {
	name := filepath.Base(path)

	seconds, err := strconv.ParseInt(name[strings.LastIndexByte(name, '.')+1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	return time.Unix(seconds, 0), true
}

// archiveTime is the timeOf an archive of pack,
// tarantool-core-<PID>-<TIME>-<HOST>.tar.gz: TIME, to the minute, in the
// local time of the host, the end of the minute, so that the crash of any
// second of it is within the window.
func archiveTime(path string, _ os.FileInfo) (time.Time, bool) {
	parts := strings.SplitN(filepath.Base(path), "-", 5) //nolint:mnd // tarantool-core-PID-TIME-.

	if len(parts) < 5 { //nolint:mnd // As above.
		return time.Time{}, false
	}

	//nolint:gosmopolitan // The pack script names the archive by the local time.
	minute, err := time.ParseInLocation(archiveTimeLayout, parts[3], time.Local)
	if err != nil {
		return time.Time{}, false
	}

	return minute.Add(time.Minute - time.Second), true
}

// locatePipe finds the core of crash tt coredump pack, the pipe of
// core_pattern with args, stored in its directory, and the archive it packed
// of it, by the time of the dump in their names: the core
// <comm>-core.<PID>.<TIME> and the archive
// tarantool-core-<PID>-<TIME>-<HOST>.tar.gz. An archive whose core is gone
// is a SourceArchive.
func locatePipe(crash sdk.Crash, args []string) (Core, error) {
	dir := packDir(args)
	pid := strconv.Itoa(crash.PID)

	stored, err := newestNear(filepath.Join(escapeGlob(dir), "*-core."+pid+".*"), crash,
		coreSlack, storedTime, true)
	if err != nil {
		return stored.found, err
	}

	// An archive is named by the PID alone: its core is not read.
	archive, err := newestNear(
		filepath.Join(escapeGlob(dir), "tarantool-core-"+pid+"-*.tar.gz"), crash,
		time.Minute, archiveTime, false)
	if err != nil {
		return archive.found, err
	}

	switch {
	case stored.found.Path != "":
		stored.found.Source = SourcePipe
		stored.found.Archive = archive.found.Path

		return stored.found, nil
	case stored.foreign.Path != "":
		return Core{}, stored.foreignErr(crash)
	case archive.found.Path != "":
		archive.found.Source = SourceArchive

		return archive.found, nil
	case archive.other.Path != "":
		return Core{}, fmt.Errorf("%w: %s is of another crash of the PID %d", ErrCoreNotFound,
			archive.other.Path, crash.PID)
	default:
		return Core{}, fmt.Errorf("%w: kernel.core_pattern pipes the cores to tt coredump "+
			"pack, and %s has neither the core of the PID %d nor its archive", ErrCoreNotFound,
			dir, crash.PID)
	}
}

// locateApport finds the core of crash of inst apport kept (see
// apportCore): in the directory of the cores of apport, or, of an older
// apport, in the working directory of the process, as the kernel writes
// core, core.PID, or core of a kernel.core_uses_pid of 0. Without one, a
// report of apport in /var/crash written about then is named: apport-unpack
// unpacks the core of it.
func (l *Locator) locateApport(inst sdk.LocalInstance, crash sdk.Crash) (Core, error) {
	core, kept, err := l.apportCore(crash)
	if err != nil || core.Path != "" {
		return core, err
	}

	name := "core"
	if l.pattern.usesPID {
		name = "core." + strconv.Itoa(crash.PID)
	}

	legacy := filepath.Join(inst.WorkDir, name)

	core, err = locateGlob(SourceApport, escapeGlob(legacy), crash, coreWindow)
	if err == nil || errors.Is(err, ErrOtherProcess) {
		return core, err
	}

	report, reportErr := newestNear(filepath.Join(escapeGlob(l.crashDir), "*.crash"),
		crash, coreWindow, modTime, false)
	if reportErr == nil && report.found.Path != "" {
		return Core{}, fmt.Errorf("%w in %s or %s; apport may have reported the crash in %s: "+
			"apport-unpack it, and pack its CoreDump with tt coredump pack", ErrCoreNotFound,
			l.apportDir, inst.WorkDir, report.found.Path)
	}

	return Core{}, fmt.Errorf("%w: kernel.core_pattern pipes the cores to apport, which kept "+
		"no core of the PID %d in %s%s, nor %s, and wrote no report into %s", ErrCoreNotFound,
		crash.PID, l.apportDir, kept, legacy, l.crashDir)
}

// apportFields is how many fields, apart by dots, the name of a core apport
// keeps has: core, the executable, the UID, the boot ID, the PID and the
// start time.
const apportFields = 6

// apportCore returns the core of crash apport kept in its directory of the
// cores, l.apportDir, core.<executable>.<UID>.<boot ID>.<PID>.<start
// time>, as get_core_path of apport.fileutils names it: the executable is
// its path with / and . made _, so the PID is the fifth field. The core is
// of this boot, and the one written last within coreWindow of the crash,
// either way: apport writes it once the kernel has dumped the core, which
// the watchdog may see the process end before. None is no error and no
// core, with what the directory has of the PID, for the error of
// locateApport to say; the core of another process of the PID is an error.
func (l *Locator) apportCore(crash sdk.Crash) (Core, string, error) {
	pid := strconv.Itoa(crash.PID)

	paths, err := filepath.Glob(filepath.Join(escapeGlob(l.apportDir), "core.*."+pid+".*"))
	if err != nil {
		return Core{}, "", fmt.Errorf("cannot look for the core in %s: %w", l.apportDir, err)
	}

	// The executable, the UID and the boot ID have no dot.
	paths = slices.DeleteFunc(paths, func(path string) bool {
		fields := strings.Split(filepath.Base(path), ".")

		return len(fields) != apportFields || fields[4] != pid ||
			l.bootID != "" && fields[3] != l.bootID
	})

	near := newestAmong(paths, crash, coreWindow, modTime, true)

	if near.found.Path != "" {
		near.found.Source = SourceApport

		return near.found, "", nil
	}

	err = near.foreignErr(crash)
	if err != nil {
		return Core{}, "", err
	}

	var none Core

	if near.other.Path != "" {
		return none, " of the time of the crash (" + filepath.Base(near.other.Path) +
			" is of another)", nil
	}

	return none, "", nil
}

// NewestFirst returns crashes, the newest first.
func NewestFirst(crashes []sdk.Crash) []sdk.Crash {
	sorted := slices.Clone(crashes)

	slices.SortStableFunc(sorted, func(a, b sdk.Crash) int { return b.At.Compare(a.At) })

	return sorted
}
