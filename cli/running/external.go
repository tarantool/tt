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
)

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
// its binary, arguments and working directory, and the pid file its watchdog
// writes for it, with no sockets or config.
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
// to stop that systemd sends too.
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
		Path:              inst.Binary,
		Args:              slices.Clone(inst.Args),
		Env:               env,
		Dir:               inst.Workdir,
		Stdin:             nil,
		Stdout:            opts.stdout,
		Stderr:            opts.stderr,
		StopSignal:        syscall.SIGTERM,
		ForwardStopSignal: false,
		StopTimeout:       instanceStopTimeout,
		ProcessGroup:      true,
		// tt logrotate reaches the process group as SIGHUP, as it reaches
		// tarantool.
		ReloadSignal:        0,
		ForwardReloadSignal: true,
	}, nil
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

	if alive {
		pgid, err := syscall.Getpgid(pid)
		if err == nil && pgid == pid {
			err = syscall.Kill(-pid, syscall.SIGKILL)
			if err != nil {
				log.Warnf("cannot kill the process group %d of %s: %s", pid,
					GetAppInstanceName(*run), err)
			}
		}
	}

	_, err = pidfile.RemoveFor(run.ProcessPIDFile, pid, wait, nil)
	if err != nil {
		log.Warnf("cannot remove the pid file %q: %s", run.ProcessPIDFile, err)
	}
}
