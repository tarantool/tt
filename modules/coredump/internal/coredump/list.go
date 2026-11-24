package coredump

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tarantool/tt/sdk"
)

// signalNames are the names of the signals a crash of an instance is told
// by, as kill -l names them; another is "signal N".
var signalNames = map[syscall.Signal]string{
	syscall.SIGABRT: "SIGABRT",
	syscall.SIGALRM: "SIGALRM",
	syscall.SIGBUS:  "SIGBUS",
	syscall.SIGFPE:  "SIGFPE",
	syscall.SIGHUP:  "SIGHUP",
	syscall.SIGILL:  "SIGILL",
	syscall.SIGINT:  "SIGINT",
	syscall.SIGKILL: "SIGKILL",
	syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGSYS:  "SIGSYS",
	syscall.SIGTERM: "SIGTERM",
	syscall.SIGTRAP: "SIGTRAP",
	syscall.SIGUSR1: "SIGUSR1",
	syscall.SIGUSR2: "SIGUSR2",
	syscall.SIGXCPU: "SIGXCPU",
	syscall.SIGXFSZ: "SIGXFSZ",
}

// SignalName returns the name of signal, SIGSEGV, or "signal N" of one
// signalNames does not name.
func SignalName(signal syscall.Signal) string {
	name, ok := signalNames[signal]
	if !ok {
		return "signal " + strconv.Itoa(int(signal))
	}

	return name
}

// CrashList is what tt coredump list prints: the instances, each with its
// crashes, the newest first, and where their cores are.
type CrashList []ListedInstance

// ListedInstance is an instance of a CrashList.
type ListedInstance struct {
	// Instance is the name of the instance, <app>:<instance>.
	Instance string `json:"instance" yaml:"instance"`
	// Crashes are its crashes, the newest first.
	Crashes []ListedCrash `json:"crashes" yaml:"crashes"`
	// CrashesError is why its crashes cannot be read, as those of another
	// user; empty when they can.
	CrashesError string `json:"crashes_error,omitempty" yaml:"crashes_error,omitempty"`
}

// ListedCrash is a crash of a ListedInstance and where its core is.
type ListedCrash struct {
	// At is when the watchdog saw the process end.
	At time.Time `json:"at" yaml:"at"`
	// PID is the PID of the process.
	PID int `json:"pid" yaml:"pid"`
	// Signal is the signal the process ended by, and SignalName its name.
	Signal     int    `json:"signal"      yaml:"signal"`
	SignalName string `json:"signal_name" yaml:"signal_name"`
	// CoreDumped tells that the kernel dumped a core of the process.
	CoreDumped bool `json:"core_dumped" yaml:"core_dumped"`
	// Core is where the core is, nil when it is not found.
	Core *ListedCore `json:"core,omitempty" yaml:"core,omitempty"`
	// Missing is why the core is not found, empty when it is.
	Missing string `json:"missing,omitempty" yaml:"missing,omitempty"`
}

// ListedCore is where the core of a ListedCrash is (Core).
type ListedCore struct {
	// Source is how it was found: file, coredumpctl, tt-pipe, tt-archive or
	// apport.
	Source string `json:"source" yaml:"source"`
	// Path is the core file, or the archive of tt-archive; empty for a core
	// of coredumpctl, which dumps it by the PID.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Size is its size in bytes, 0 when coredumpctl tells none.
	Size int64 `json:"size" yaml:"size"`
	// Archive is the archive the pipe of tt-pipe packed of the core.
	Archive string `json:"archive,omitempty" yaml:"archive,omitempty"`
}

// List returns the crashes of instances, each instance in its order with its
// crashes the newest first, and where loc finds their cores.
func List(ctx context.Context, loc *Locator, instances []sdk.LocalInstance) CrashList {
	list := make(CrashList, 0, len(instances))

	for _, inst := range instances {
		listed := ListedInstance{Instance: inst.Name, Crashes: []ListedCrash{}, CrashesError: ""}

		if inst.CrashesErr != nil {
			listed.CrashesError = inst.CrashesErr.Error()
		}

		for _, crash := range NewestFirst(inst.Crashes) {
			listed.Crashes = append(listed.Crashes, listCrash(ctx, loc, inst, crash))
		}

		list = append(list, listed)
	}

	return list
}

// listCrash returns crash of inst, with where loc finds its core.
func listCrash(ctx context.Context, loc *Locator, inst sdk.LocalInstance, crash sdk.Crash,
) ListedCrash {
	listed := ListedCrash{
		At: crash.At, PID: crash.PID, Signal: int(crash.Signal),
		SignalName: SignalName(crash.Signal), CoreDumped: crash.CoreDumped, Core: nil,
		Missing: "",
	}

	core, err := loc.Locate(ctx, inst, crash)
	if err != nil {
		listed.Missing = err.Error()

		return listed
	}

	listed.Core = &ListedCore{
		Source: core.Source, Path: core.Path, Size: core.Size, Archive: core.Archive,
	}

	return listed
}

// Human prints the crashes as a table, the newest first in each instance:
// the instance, when, in the time zone of the watchdog that recorded it, the
// local time of the host, as tt instance status tells it, the PID, the
// signal and where the core is. It prints nothing when no instance has a
// crash.
func (list CrashList) Human(w io.Writer) error {
	if len(list.Crashed()) == 0 {
		return nil
	}

	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // Two spaces apart.

	_, _ = fmt.Fprintln(table, "INSTANCE\tCRASHED AT\tPID\tSIGNAL\tCORE")

	for _, inst := range list {
		for _, crash := range inst.Crashes {
			_, _ = fmt.Fprintf(table, "%s\t%s\t%d\t%s\t%s\n", inst.Instance,
				crash.At.Format(time.DateTime), crash.PID, crash.SignalName,
				describeCore(crash))
		}
	}

	err := table.Flush()
	if err != nil {
		return fmt.Errorf("printing the crashes: %w", err)
	}

	return nil
}

// Crashed returns the names of the instances of the list that have a crash.
func (list CrashList) Crashed() []string {
	var names []string

	for _, inst := range list {
		if len(inst.Crashes) > 0 {
			names = append(names, inst.Instance)
		}
	}

	return names
}

// NotCrashed returns the names of the instances of the list with no crash,
// of those whose crashes can be read.
func (list CrashList) NotCrashed() []string {
	var names []string

	for _, inst := range list {
		if len(inst.Crashes) == 0 && inst.CrashesError == "" {
			names = append(names, inst.Instance)
		}
	}

	return names
}

// Unreadable returns the instances of the list whose crashes cannot be read,
// in order, each with why.
func (list CrashList) Unreadable() []ListedInstance {
	var unreadable []ListedInstance

	for _, inst := range list {
		if inst.CrashesError != "" {
			unreadable = append(unreadable, inst)
		}
	}

	return unreadable
}

// HasCore reports whether a crash of the list has a core found.
func (list CrashList) HasCore() bool {
	for _, inst := range list {
		for _, crash := range inst.Crashes {
			if crash.Core != nil {
				return true
			}
		}
	}

	return false
}

// describeCore tells where the core of crash is, with its size, or why it is
// not found.
func describeCore(crash ListedCrash) string {
	core := crash.Core

	switch {
	case !crash.CoreDumped:
		return "none dumped"
	case core == nil:
		return "not found: " + strings.TrimPrefix(crash.Missing, ErrCoreNotFound.Error()+": ")
	case core.Source == SourceCoredumpctl:
		described := "kept by systemd-coredump: coredumpctl dump " + strconv.Itoa(crash.PID)
		if core.Size > 0 {
			described += ", " + FormatSize(core.Size)
		}

		return described
	case core.Source == SourceArchive:
		return core.Path + ", " + FormatSize(core.Size) + ", packed by the core_pattern pipe"
	case core.Archive != "":
		return core.Path + ", " + FormatSize(core.Size) + ", packed into " + core.Archive
	default:
		return core.Path + ", " + FormatSize(core.Size)
	}
}

// FormatSize returns size in bytes as people read it: 512 B, 1.5 KiB,
// 1.1 GiB. A value is shown to a tenth, and one that rounds to 1024.0 of a
// unit is shown in the next one: 1048556 bytes, 1023.98 KiB, is 1.0 MiB.
func FormatSize(size int64) string {
	const (
		unit = 1024
		// last is the largest value shown in a unit, rounded to a tenth:
		// 1023.95 rounds to 1024.0.
		last = unit - 0.05
	)

	if size < unit {
		return strconv.FormatInt(size, 10) + " B"
	}

	value := float64(size) / unit

	for _, prefix := range []string{"Ki", "Mi", "Gi", "Ti"} {
		if value < last {
			return fmt.Sprintf("%.1f %sB", value, prefix)
		}

		value /= unit
	}

	return fmt.Sprintf("%.1f PiB", value)
}
