package running

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"time"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/pidfile"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// ErrNotTarantool is wrapped by the error for an external process given to a
// command that has something to do only with a Tarantool instance, such as
// tt connect: an external process has no sockets to reach, no config and no
// data.
var ErrNotTarantool = errors.New("not a Tarantool instance")

var (
	// errNoExecutable is wrapped by the error for an external process whose
	// binary is not an executable, or is not found in PATH.
	errNoExecutable = errors.New("cannot run the binary")
	// errNoWorkdir is wrapped by the error for an external process whose
	// working directory is not a directory.
	errNoWorkdir = errors.New("cannot run in the working directory")
	// errKindChanged is wrapped by the error for an instance whose record
	// has turned into another kind of instance while its watchdog runs.
	errKindChanged = errors.New("the instance has changed its kind")
	// errNotDirectory is wrapped by the error for a working directory that
	// is a file.
	errNotDirectory = errors.New("is not a directory")
	// errOrphan is wrapped by the error for an external process whose
	// process runs without its watchdog (Orphan).
	errOrphan = errors.New("the process of an earlier start still runs")
)

// orphanPollInterval is how often tt stop and tt kill look whether the
// process of an external process without its watchdog is gone.
const orphanPollInterval = 50 * time.Millisecond

// IsExternal reports whether inst is an external process, a record with
// binary, rather than a Tarantool instance.
func IsExternal(inst *InstanceCtx) bool {
	return inst.Kind == instances.KindExternal
}

// RequireTarantool returns an error wrapping ErrNotTarantool when inst is an
// external process.
func RequireTarantool(inst *InstanceCtx) error {
	if !IsExternal(inst) {
		return nil
	}

	return notTarantoolError(inst.AppName, inst.InstName, inst.Binary)
}

// RequireTarantoolRecord returns an error wrapping ErrNotTarantool when the
// record of application named name is an external process. It is for a
// command that takes an instance by its name without acting on it, such as
// tt cluster show: a name the description does not declare passes, since
// such a command may name an instance that runs on another host.
func RequireTarantoolRecord(application *app.App, name string) error {
	for _, record := range application.Instances {
		if record.Name == name && record.Kind() == instances.KindExternal {
			return notTarantoolError(application.Name, name, record.Binary)
		}
	}

	return nil
}

func notTarantoolError(appName, instName, binary string) error {
	return fmt.Errorf("%s%c%s is an external process (binary: %s): %w",
		appName, InstanceDelimiter, instName, binary, ErrNotTarantool)
}

// kindName names kind for a message.
func kindName(kind instances.Kind) string {
	switch kind {
	case instances.KindExternal:
		return "an external process"
	case instances.KindScript:
		return "a Tarantool instance that runs its script"
	case instances.KindCluster:
	}

	return "a Tarantool instance"
}

// setExternalCtx makes instCtx, built for the record inst with the paths of
// a Tarantool instance, the context of the external process inst describes:
// its binary, arguments and working directory, its logrotate_signal, and the
// pid file its watchdog writes for it, with no sockets or config.
//
// Without workdir the process runs in its data directory in the layout,
// <root>/var/lib/<app>/<inst>/, in every scope (decision 33): the
// application directory of an installed application is its version, which
// is never written to, and what the process writes there would be lost with
// the version. That directory is the one data directory it has, created at
// its start and emptied by tt clean; a process with workdir has none.
func setExternalCtx(instCtx *InstanceCtx, inst instances.Instance, scopeLayout layout.ScopeLayout) {
	instCtx.Binary = inst.Binary
	instCtx.Args = slices.Clone(inst.Args)
	instCtx.LogrotateSignal = inst.LogrotateSignal

	instCtx.Workdir = inst.Workdir
	if instCtx.Workdir == "" {
		instCtx.DataDir = scopeLayout.DataDir()
		instCtx.Workdir = instCtx.DataDir
	}

	instCtx.ProcessPIDFile = scopeLayout.ProcessPidFile()
	instCtx.WalDir = ""
	instCtx.MemtxDir = ""
	instCtx.VinylDir = ""
	instCtx.ConsoleSocket = ""
	instCtx.BinaryPort = ""
	instCtx.ClusterConfigPath = ""
}

// checkExternal checks what has to hold before an external process starts:
// its binary is an executable, found in PATH of tt when the record names a
// program rather than a path, and the workdir of its record is a directory.
//
// The command that starts the process checks it before it detaches the
// watchdog (IsAbleToStartInstances), so that a typo in the description is
// reported to the user rather than only in the log of the watchdog; the
// watchdog checks it again before every start, as the binary may be gone by
// then. The workdir of a record is not created: a directory the description
// names and that does not exist is more likely a mistake than a request.
// The data directory, the working directory without workdir, is tt's to
// create (externalSpec).
func checkExternal(inst *InstanceCtx) error {
	_, err := exec.LookPath(inst.Binary)
	if err != nil {
		return fmt.Errorf("instance %q of %q: %w: %w", inst.InstName, inst.AppName,
			errNoExecutable, err)
	}

	if inst.Workdir == inst.DataDir {
		return nil
	}

	info, err := os.Stat(inst.Workdir)
	if err == nil && !info.IsDir() {
		err = fmt.Errorf("%s %w", inst.Workdir, errNotDirectory)
	}

	if err != nil {
		return fmt.Errorf("instance %q of %q: %w: %w", inst.InstName, inst.AppName,
			errNoWorkdir, err)
	}

	return nil
}

// externalSpec is the Spec of an external process: its binary runs with the
// arguments of its record in its working directory, created when it is the
// data directory, with the env of the record on top of the inherited
// environment. tt adds nothing to it but PWD, which a program reads its
// working directory from, and which the record may still override. A binary
// named without a slash is looked up in PATH of tt as the process starts,
// not in PATH the env of the record may set.
//
// The process stops with SIGTERM, unless its record names another stop
// signal, and is killed after instanceStopTimeout, as a Tarantool instance
// is, unless its record allows another (applyStopSettings). The stop signal
// the watchdog receives is not forwarded: SIGINT, which tt stop sends it, is
// an interrupt from a terminal to most programs, while SIGTERM is the request
// to stop that systemd sends too (decision 31).
//
// tt logrotate makes the watchdog reopen tt.log, where the output of the
// process goes, and sends the process its logrotate_signal; without one the
// process gets nothing (decision 32), since SIGHUP ends a program that does
// not handle it.
//
// The process leads a process group of its own, and every signal goes to the
// whole group: an external process may be a wrapper, such as a shell script
// that runs the actual program, and a signal to the wrapper alone would leave
// the program running. tt kill, which kills the group of the watchdog, finds
// the group of the process through its pid file (killProcess).
func externalSpec(inst *InstanceCtx, opts specOptions) (supervisor.Spec, error) {
	err := checkExternal(inst)
	if err != nil {
		return supervisor.Spec{}, err
	}

	if inst.DataDir != "" {
		err = util.CreateDirectory(inst.DataDir, defaultDirPerms)
		if err != nil {
			return supervisor.Spec{}, fmt.Errorf("instance %q of %q: %w", inst.InstName,
				inst.AppName, err)
		}
	}

	env := append(os.Environ(), "PWD="+inst.Workdir)

	for _, name := range slices.Sorted(maps.Keys(inst.Env)) {
		env = append(env, name+"="+inst.Env[name])
	}

	return supervisor.Spec{
		Path:                inst.Binary,
		Args:                slices.Clone(inst.Args),
		Env:                 env,
		Dir:                 inst.Workdir,
		Stdin:               nil,
		Stdout:              opts.stdout,
		Stderr:              opts.stderr,
		StopSignal:          syscall.SIGTERM,
		ForwardStopSignal:   false,
		StopTimeout:         instanceStopTimeout,
		ProcessGroup:        true,
		ReloadSignal:        inst.LogrotateSignal,
		ForwardReloadSignal: false,
	}, nil
}

// leadsGroup reports whether the process pid leads its process group, as an
// external process does (externalSpec).
func leadsGroup(pid int) bool {
	pgid, err := syscall.Getpgid(pid)

	return err == nil && pgid == pid
}

// Orphan returns the pid of the process of the external process inst that
// runs without its watchdog, or zero: tt.pid names no live watchdog, while
// process.pid, which the watchdog wrote, names a live process that leads its
// process group, as the process does.
//
// A watchdog killed by anything but tt kill leaves its process running in its
// group, which killing the group of the watchdog does not reach. Such a
// process is not the instance running, since nothing supervises it, and not
// an instance stopped either: tt start refuses to start a second copy next
// to it, tt stop and tt kill stop it, and tt status reports it.
//
// A pid that leads no group is not the process, which leads its own; a pid
// reused by another group leader, which nothing tells from the process, is
// taken for it.
func Orphan(inst *InstanceCtx) int {
	if inst.ProcessPIDFile == "" {
		return 0
	}

	if process_utils.ProcessStatus(inst.PIDFile).Code == process_utils.ProcessRunningCode {
		return 0
	}

	pid, alive, err := pidfile.Alive(inst.ProcessPIDFile)
	if err != nil || !alive || !leadsGroup(pid) {
		return 0
	}

	return pid
}

// checkOrphan refuses to start an external process whose process runs
// without its watchdog (Orphan).
func checkOrphan(inst *InstanceCtx) error {
	pid := Orphan(inst)
	if pid == 0 {
		return nil
	}

	name := GetAppInstanceName(*inst)

	return fmt.Errorf("instance %q of %q: %w: its process (PID = %d, %s) runs without "+
		"its watchdog, and a start would run a second one; stop it with tt stop %s or "+
		"tt kill %s, then start the instance", inst.InstName, inst.AppName, errOrphan, pid,
		inst.ProcessPIDFile, name, name)
}

// stopOrphan stops the process pid of the external process of run that runs
// without its watchdog (Orphan), as the watchdog would have stopped it: its
// process group gets its stop signal, and SIGKILL if the process has not
// exited by its stop timeout. The pid files the watchdog left go then.
func stopOrphan(run *InstanceCtx, pid int) error {
	sig := run.StopSignal
	if sig == 0 {
		sig = syscall.SIGTERM
	}

	timeout := run.StopTimeout
	if timeout <= 0 {
		timeout = instanceStopTimeout
	}

	err := syscall.Kill(-pid, sig)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("cannot stop the process group %d of %s: %w", pid,
			GetAppInstanceName(*run), err)
	}

	if !waitGone(pid, timeout) {
		log.Warnf("The process of %s (PID = %v) did not stop in %s, killing it.",
			GetAppInstanceName(*run), pid, timeout)

		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = waitGone(pid, killCleanupTimeout)
	}

	removeLeftPidFiles(run, pid)

	log.Infof("The process of %s (PID = %v), left without its watchdog, has been stopped.",
		GetAppInstanceName(*run), pid)

	return nil
}

// killOrphan kills the process group of the process pid of the external
// process of run that runs without its watchdog (Orphan), and removes the
// pid files the watchdog left.
func killOrphan(run *InstanceCtx, pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to kill the process group %d of %s: %w", pid,
			GetAppInstanceName(*run), err)
	}

	_ = waitGone(pid, killCleanupTimeout)

	removeLeftPidFiles(run, pid)

	log.Infof("The process of %s (PID = %v), left without its watchdog, has been killed.",
		GetAppInstanceName(*run), pid)

	return nil
}

// waitGone waits up to timeout for the process pid to be gone, and reports
// whether it is.
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for {
		alive, _ := process_utils.IsProcessAlive(pid)
		if !alive {
			return true
		}

		if time.Now().After(deadline) {
			return false
		}

		time.Sleep(orphanPollInterval)
	}
}

// removeLeftPidFiles removes the pid files a dead watchdog left: process.pid
// while it names pid, the process just stopped, and tt.pid while it names a
// process that is gone. A file that names something else is a newer one,
// and stays.
func removeLeftPidFiles(run *InstanceCtx, pid int) {
	_, err := pidfile.RemoveFor(run.ProcessPIDFile, pid, killCleanupTimeout, nil)
	if err != nil {
		log.Warnf("cannot remove the pid file %q: %s", run.ProcessPIDFile, err)
	}

	watchdog, alive, err := pidfile.Alive(run.PIDFile)
	if err != nil || watchdog == 0 || alive {
		return
	}

	_, err = pidfile.RemoveFor(run.PIDFile, watchdog, killCleanupTimeout, nil)
	if err != nil {
		log.Warnf("cannot remove the pid file %q: %s", run.PIDFile, err)
	}
}

// killProcess kills the process group of the external process of run, after
// tt kill has killed the watchdog: the group of the watchdog does not hold
// the process, which leads a group of its own (externalSpec).
//
// The pid file is read once the watchdog is dead, so that it no longer
// changes. The process leads its group, so a pid that leads none is no
// longer the process, and is left alone. The pid file goes once the kernel
// has dropped the lock of the watchdog, which may take up to wait.
func killProcess(run *InstanceCtx, wait time.Duration) {
	if run.ProcessPIDFile == "" {
		return
	}

	pid, alive, err := pidfile.Alive(run.ProcessPIDFile)
	if err != nil {
		log.Warnf("cannot read the pid file %q: %s", run.ProcessPIDFile, err)

		return
	}

	if pid == 0 {
		return
	}

	if alive && leadsGroup(pid) {
		err = syscall.Kill(-pid, syscall.SIGKILL)
		if err != nil {
			log.Warnf("cannot kill the process group %d of %s: %s", pid,
				GetAppInstanceName(*run), err)
		}
	}

	_, err = pidfile.RemoveFor(run.ProcessPIDFile, pid, wait, nil)
	if err != nil {
		log.Warnf("cannot remove the pid file %q: %s", run.ProcessPIDFile, err)
	}
}
