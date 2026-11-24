package sdk

import (
	"syscall"
	"time"
)

// LocalInstance is an instance of an application of this host, as tt runs
// it ([Services.LocalInstances]): where its process runs, where tt keeps its
// files and its logs, and the crashes of its process. It describes the
// instance whether it runs or not.
type LocalInstance struct {
	// Name is the name of the instance, <app>:<instance>.
	Name string
	// WorkDir is the working directory of the process of the instance,
	// where a core the kernel dumps by a relative kernel.core_pattern goes:
	// the application directory of a Tarantool instance, or the work_dir of
	// its box.cfg, process.work_dir of the config, or TT_WORK_DIR of its
	// record, relative to it; the workdir of an external process. What a
	// script passes to box.cfg itself is beyond tt.
	WorkDir string
	// RunDir is the run directory of the instance, where its watchdog keeps
	// its pid file, its sockets and its records.
	RunDir string
	// Log is the log of the watchdog of the instance, tt.log, which has the
	// standard output and error of the process too: the log of a Tarantool
	// instance that logs to its standard error, as by default, and the
	// output of an external process.
	Log string
	// TarantoolLog is the file a Tarantool instance logs into of its own:
	// log.file of an instance whose config has log.to file, tarantool.log
	// next to Log by default, or the file TT_LOG of the record of one that
	// runs its script names. It is empty for an instance that logs to its
	// standard error, into Log, or to a pipe or syslog, and for an external
	// process.
	TarantoolLog string
	// Crashes are the exits of the process of the instance by a signal, as
	// its watchdogs recorded them through its restarts and stops, the oldest
	// first: the last ten of them. They are none when CrashesErr is set.
	Crashes []Crash
	// CrashesErr is why the crashes of the instance cannot be read, nil
	// when they can, or when none is recorded: the record is one this
	// process may not read, as that of an instance of another user, which
	// its watchdog writes for its own user alone, one that is broken, or
	// one a newer tt wrote. It is the instance's alone: the others are
	// listed all the same.
	CrashesErr error
}

// Crash is an exit of the process of an instance by a signal, as its
// watchdog recorded it.
type Crash struct {
	// At is when the watchdog saw the process end, a moment after the
	// kernel dumped its core, in the local time of the host.
	At time.Time
	// PID is the PID of the process, as %p of kernel.core_pattern gives it.
	PID int
	// Signal is the signal the process ended by.
	Signal syscall.Signal
	// CoreDumped tells that the kernel dumped a core of the process: to a
	// file, or to the program of a kernel.core_pattern pipe.
	CoreDumped bool
}
