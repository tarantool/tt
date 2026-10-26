package running

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
	"time"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/ttlog"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/cli/util/regexputil"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/pidfile"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

var (
	errCannotGetConfigValueAtAs = errors.New(
		"cannot get config value at ",
	)
	errInstanceDead = errors.New(
		process_utils.ProcStateDead.String(),
	)
	errLoggerChangedCheckFailedPassingNullAsAnInstanceContext = errors.New(
		"logger changed check failed: passing null as an instance context",
	)
	errTheInstanceIsNotRunningItMustBeStarted = errors.New(
		": the instance is not running, it must be started",
	)
)

const (
	defaultDirPerms        = 0o770
	instanceCleanupTimeout = 10 * time.Second
	watchdogRestartTimeout = 5 * time.Second
	// killCleanupTimeout bounds the wait for the kernel to drop the pid file
	// lock of a killed watchdog.
	killCleanupTimeout = 5 * time.Second
)

// The hidden flags of tt start that make it the watchdog of one instance
// (StartWatchdog), and pin the application the watchdog runs the instance
// from: the watchdog must find the same application directory that the
// command that started it found, not whatever <root>/apps/<app> points to by
// the time it comes up.
const (
	// WatchdogFlag makes tt start run the watchdog of its one target.
	WatchdogFlag = "watchdog"
	// ScopeFlag is the scope of the application.
	ScopeFlag = "scope"
	// TemplateFlag is the template the instance was resolved with.
	TemplateFlag = "template"
	// AppDirFlag pins the application directory (app.Options.Dir).
	AppDirFlag = "app-dir"
)

type syntaxCheckError string

func (err syntaxCheckError) Error() string {
	return string(err)
}

// RunningCtx contains information about application instances.
type RunningCtx struct {
	// Instances contains information about application instances.
	Instances []InstanceCtx
}

// InstanceCtx contains information about application instance.
type InstanceCtx struct {
	// AppDir is the application directory, the working directory of the
	// instance: the project directory, or the version directory
	// <root>/apps/<app> pointed to when the application was resolved.
	AppDir string
	// InstanceScript is the script of a Tarantool instance that configures
	// itself, run by scriptSpec. Nothing sets it while NewInstanceCtx
	// refuses such instances.
	InstanceScript string
	// Kind tells how the instance runs: a Tarantool instance configured by
	// the cluster config, or an external process (IsExternal).
	Kind instances.Kind
	// Binary is the executable of an external process: an absolute path, or
	// the name of a program looked up in PATH when the process starts.
	Binary string
	// Args are the arguments of an external process.
	Args []string
	// Workdir is the working directory of an external process: workdir of
	// its record, or else DataDir.
	Workdir string
	// DataDir is the data directory of an external process that runs in it,
	// one whose record sets no workdir: <root>/var/lib/<app>/<inst>/, which
	// the start creates and tt clean empties. It is empty for a process with
	// workdir and for a Tarantool instance, whose data directories are
	// WalDir, MemtxDir and VinylDir.
	DataDir string
	// AppName is the name of the application.
	AppName string
	// Instance name.
	InstName string
	// Directory that stores various instance runtime artifacts like
	// console socket, PID file, etc.
	RunDir string
	// Directory that stores log files.
	LogDir string
	// Log is the name of log file.
	Log string
	// WalDir is a directory where write-ahead log (.xlog) files are stored.
	WalDir string
	// MemtxDir is a directory where memtx stores snapshot (.snap) files.
	MemtxDir string `mapstructure:"memtx_dir" yaml:"memtx_dir"`
	// VinylDir is a directory where vinyl files or subdirectories will be stored.
	VinylDir string `mapstructure:"vinyl_dir" yaml:"vinyl_dir"`
	// LogMaxSize is the maximum size in megabytes of the log file
	// before it gets rotated. It defaults to 100 megabytes.
	LogMaxSize int
	// LogMaxBackups is the maximum number of old log files to retain.
	// The default is to retain all old log files (though LogMaxAge may
	// still cause them to get deleted).
	LogMaxBackups int
	// LogMaxAge is the maximum number of days to retain old log files
	// based on the timestamp encoded in their filename. Note that a
	// day is defined as 24 hours and may not exactly correspond to
	// calendar days due to daylight savings, leap seconds, etc. The
	// default is not to remove old log files based on age.
	LogMaxAge int
	// The name of the file with the watchdog PID under which the
	// instance was started.
	PIDFile string
	// ProcessPIDFile is the pid file of an external process, which its
	// watchdog writes; empty for a Tarantool instance.
	ProcessPIDFile string
	// Restart is the restart policy of the instance record, which the
	// watchdog follows after the instance exits on its own (restartsAfter).
	Restart instances.RestartPolicy
	// StopSignal is the signal the instance record stops the instance
	// with. Zero leaves tarantool the stop signal the watchdog receives,
	// SIGINT from tt stop, and stops an external process with SIGTERM.
	StopSignal syscall.Signal
	// StopTimeout is how long the instance record lets the instance take to
	// stop before it is killed. Zero is instanceStopTimeout.
	StopTimeout time.Duration
	// Control UNIX socket for started instance.
	ConsoleSocket string
	// Unix socket used as "binary port".
	BinaryPort string
	// ClusterConfigPath is the cluster config of the pair of the
	// application, the file tarantool -c gets.
	ClusterConfigPath string
	// Configuration is instance configuration loaded from cluster config.
	// Nil when no cluster config is loaded.
	Configuration *goconfig.Config
	// Scope is the scope of the application.
	Scope app.Scope
	// Root is the root of that scope, which the runtime artifacts of the
	// instance are under (layout.ScopeLayout).
	Root string
	// Template is the --template value the instance was resolved with,
	// empty for none: the watchdog resolves the instance again with it
	// (PinnedOptions).
	Template string
	// Env holds the variables the instance record sets on top of the
	// inherited environment of tarantool.
	Env map[string]string
}

// removeSockets removes the console socket and the binary port socket of an
// instance that is gone. The pid file is not among them: only its owner, or
// pidfile.RemoveFor, removes it.
func removeSockets(run *InstanceCtx) {
	_, err := os.Stat(run.ConsoleSocket)
	if err == nil {
		_ = os.Remove(run.ConsoleSocket)
	}

	_, err = os.Stat(run.BinaryPort)
	if err == nil {
		err = os.Remove(run.BinaryPort)
		if err != nil {
			log.Warnf("unable to remove binary port: %q: %s", run.BinaryPort, err)
		}
	}
}

// createLogger prepares a logger for the watchdog and instance.
func createLogger(run *InstanceCtx) (ttlog.Logger, error) {
	opts := ttlog.LoggerOpts{
		Filename: run.Log,
		Prefix:   "Watchdog ",
	}

	return ttlog.NewFileLogger(opts)
}

// renderInstCtxMembers instantiates some members of instance context.
func renderInstCtxMembers(instance *InstanceCtx) error {
	templateData := map[string]string{
		"instance_name": instance.InstName,
	}
	for _, dstString := range []*string{
		&instance.WalDir, &instance.VinylDir, &instance.MemtxDir, &instance.ConsoleSocket,
		&instance.BinaryPort,
	} {
		renderedString, err := regexputil.ApplyVars(*dstString, templateData)
		if err != nil {
			return fmt.Errorf("error instantiating template: %w", err)
		}

		*dstString = renderedString
	}

	return nil
}

// createInstanceDataDirectories creates directories for data and runtime
// artifacts. An external process has a data directory only when it runs in
// it (DataDir).
func createInstanceDataDirectories(instance InstanceCtx) error {
	for _, dataDir := range [...]string{
		instance.WalDir, instance.VinylDir,
		instance.MemtxDir, instance.RunDir, instance.LogDir, instance.DataDir,
	} {
		if dataDir == "" {
			continue
		}

		err := util.CreateDirectory(dataDir, defaultDirPerms)
		if err != nil {
			return err
		}
	}

	return nil
}

// RunInstance runs tarantool instance and waits for completion.
func RunInstance(ctx context.Context, cmdCtx *cmdcontext.CmdCtx, inst InstanceCtx,
	stdOut, stdErr io.Writer,
) error {
	err := checkStart(&inst)
	if err != nil {
		return err
	}

	for _, dataDir := range [...]string{
		inst.WalDir, inst.VinylDir, inst.MemtxDir, inst.RunDir, inst.DataDir,
	} {
		if dataDir == "" {
			continue
		}

		err := util.CreateDirectory(dataDir, defaultDirPerms)
		if err != nil {
			return err
		}
	}

	logger := ttlog.NewCustomLogger(stdOut, "", 0)

	spec, err := instanceSpec(&inst, specOptions{
		integrity: integrityOf(cmdCtx),
		stdout:    stdOut,
		stderr:    stdErr,
		warn: func(message string) {
			logger.Println("(WARN) " + message)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create the instance %q: %w", inst.InstName, err)
	}

	logger.Println("(INFO) Start")

	// No supervisor: the instance runs until it exits or ctx, which the
	// interrupt of the terminal cancels, stops it.
	cmd := spec.Command(ctx)

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start the instance %q: %w", inst.InstName, err)
	}

	owned, err := process_utils.CreatePIDFile(inst.PIDFile, cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Signal(spec.StopSignal)

		waitOrKill(cmd, instanceCleanupTimeout)

		return fmt.Errorf("cannot create the pid file %q: %w", inst.PIDFile, err)
	}

	//nolint:contextcheck // removeSockets logs through sdk/log, which takes no context.
	defer func() {
		_ = owned.Release()

		removeSockets(&inst)
	}()

	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("waiting for the process: %w", err)
	}

	return nil
}

func waitOrKill(cmd *exec.Cmd, timeout time.Duration) {
	done := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()

		<-done
	}
}

// Start runs the watchdog of an instance, the process tt start detaches with
// --watchdog, until the supervision is over. The pid file of the instance
// names the watchdog, so tt stop, status, kill, quit and logrotate reach it;
// watchdogOptions says what it does with their signals.
//
// Before every start the watchdog reads the instance anew from the pair of
// the application directory it started from (reload): a restart after an
// exit picks up an edited config and restart policy, and stays on the
// version it started from. ctx bounds those reads, which may go to a config
// storage.
func Start(ctx context.Context, cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx) error {
	pinned := *inst

	//nolint:contextcheck // The watchdog runs until it is stopped; ctx bounds the reads.
	return superviseInstance(cmdCtx, inst, func() (InstanceCtx, error) {
		return reload(ctx, &pinned, cmdCtx.Integrity)
	})
}

// superviseInstance runs the watchdog of inst, which takes the context of
// the instance from refresh before every start and after every exit.
func superviseInstance(cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx,
	refresh func() (InstanceCtx, error),
) error {
	err := checkStart(inst)
	if err != nil {
		return err
	}

	err = createInstanceDataDirectories(*inst)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	logger, err := createLogger(inst)
	if err != nil {
		return fmt.Errorf("cannot create a logger: %w", err)
	}

	logger.Println("[INFO] Start") // Create a log file before any other actions.

	src := newInstanceSource(cmdCtx, inst, &watchdogLog{
		logger:      logger,
		checkPeriod: time.Duration(cmdCtx.Cli.IntegrityCheckPeriod) * time.Second,
	}, refresh)

	engine, err := supervisor.New(src, watchdogOptions(cmdCtx, src))
	if err != nil {
		return fmt.Errorf("cannot create the watchdog: %w", err)
	}

	src.log.onEnd(engine.Run(context.Background()))

	return nil
}

// Stop stops an instance and reports any interruption or termination error.
func Stop(run *InstanceCtx) error {
	return StopAll([]InstanceCtx{*run})
}

// StopAll interrupts all selected instances before waiting for them to exit.
// A missing pid file means the instance is already stopped. Other errors are
// collected without preventing the remaining instances from stopping.
func StopAll(runs []InstanceCtx) error {
	type instanceProcess struct {
		name string
		pid  int
		wait time.Duration
	}

	processes := make([]instanceProcess, 0, len(runs))

	var stopErrors []error

	for _, run := range runs {
		name := GetAppInstanceName(run)
		pid, err := process_utils.InterruptProcess(run.PIDFile)

		switch {
		case errors.Is(err, fs.ErrNotExist):
			log.Debugf("The instance %s is already stopped", name)
		case err != nil:
			stopErrors = append(stopErrors, fmt.Errorf("failed to stop instance %s: %w", name, err))
		default:
			processes = append(processes, instanceProcess{name: name, pid: pid, wait: stopWait(&run)})
		}
	}

	for _, proc := range processes {
		err := process_utils.WaitProcessTermination(proc.pid, proc.wait)
		if err != nil {
			stopErrors = append(stopErrors,
				fmt.Errorf("failed to stop instance %s (PID = %d): %w", proc.name, proc.pid, err))

			continue
		}

		log.Infof("The Instance %s (PID = %v) has been terminated.", proc.name, proc.pid)
	}

	return errors.Join(stopErrors...)
}

// Kill kills instance process: the process group of its watchdog, and the
// process group of an external process, which is a group of its own.
func Kill(run InstanceCtx) error {
	pid, err := process_utils.KillProcessGroup(run.PIDFile)
	if err != nil {
		return fmt.Errorf("failed to kill the processes: %w", err)
	}

	killProcess(&run, killCleanupTimeout)
	cleanupAfterKill(&run, pid, killCleanupTimeout, removeSockets)

	fullInstanceName := GetAppInstanceName(run)
	log.Infof("The instance %s (PID = %v) has been killed.", fullInstanceName, pid)

	return nil
}

// cleanupAfterKill cleans up after the watchdog pid that tt kill killed and
// that cannot clean up after itself. Its pid file goes only while it still
// names pid, once the kernel has dropped the lock, which may take up to
// wait; removeSockets runs just before, under that lock. With no pid file at
// all, the sockets go under a pid file tt kill takes for their removal.
// Either way no watchdog owns the instance while its sockets are removed: one
// that comes up in the meantime keeps its pid file and its sockets.
func cleanupAfterKill(run *InstanceCtx, pid int, wait time.Duration,
	removeSockets func(run *InstanceCtx),
) {
	cleanup := func() { removeSockets(run) }

	removed, err := pidfile.RemoveFor(run.PIDFile, pid, wait, cleanup)
	if err != nil {
		log.Warnf("cannot remove the pid file %q: %s", run.PIDFile, err)

		return
	}

	_, err = os.Stat(run.PIDFile)
	if removed || !errors.Is(err, fs.ErrNotExist) {
		return
	}

	owned, err := pidfile.Acquire(run.PIDFile, os.Getpid())

	switch {
	case errors.Is(err, pidfile.ErrBusy):
		// A watchdog has come up and owns the instance.
		return
	case err != nil:
		log.Warnf("cannot take the pid file %q: %s", run.PIDFile, err)

		return
	}

	cleanup()

	err = owned.Release()
	if err != nil {
		log.Warnf("cannot remove the pid file %q: %s", run.PIDFile, err)
	}
}

// Quit the Instance.
//
// An external process is refused: the watchdog stops it with its stop signal
// whatever stopped the watchdog, so it would stop without a dump while the
// command reported one.
func Quit(run InstanceCtx) error {
	err := RequireTarantool(&run)
	if err != nil {
		return fmt.Errorf("cannot quit with a dump: %w", err)
	}

	pid, err := process_utils.QuitProcess(run.PIDFile)
	if err != nil {
		return fmt.Errorf("failed to quit the process: %w", err)
	}

	_, err = os.Stat(run.ConsoleSocket)
	if err == nil {
		err = os.Remove(run.ConsoleSocket)
		if err != nil {
			log.Warnf("cannot remove console socket %q: %s", run.ConsoleSocket, err)
		}
	}

	fullInstanceName := GetAppInstanceName(run)
	log.Infof("The instance %s (PID = %v) has been terminated with dump.", fullInstanceName, pid)

	return nil
}

func Status(run *InstanceCtx) process_utils.ProcessState {
	return process_utils.ProcessStatus(run.PIDFile)
}

// Logrotate rotates logs of a started tarantool instance.
func Logrotate(run *InstanceCtx) error {
	fullInstanceName := GetAppInstanceName(*run)

	pid, err := process_utils.GetPIDFromFile(run.PIDFile)
	if err != nil {
		return fmt.Errorf("%s%w", fullInstanceName, errTheInstanceIsNotRunningItMustBeStarted)
	}

	alive, _ := process_utils.IsProcessAlive(pid)
	if !alive {
		return errInstanceDead
	}

	err = syscall.Kill(pid, syscall.SIGHUP)
	if err != nil {
		return fmt.Errorf(`can't rotate logs: "%w"`, err)
	}

	// Rotates logs [instance name pid].
	log.Infof("%s (PID = %v): logs has been rotated.", fullInstanceName, pid)

	return nil
}

// AppFile returns the application file of inst, the Lua file its config
// runs: app.file of its effective config, resolved against its application
// directory, which is what tarantool resolves it against. found is false
// when the config names none.
func AppFile(inst *InstanceCtx) (string, bool, error) {
	file, found, err := effectiveString(inst, "app.file")
	if err != nil || !found {
		return "", false, err
	}

	path, err := util.JoinAbspath(inst.AppDir, file)
	if err != nil {
		return "", false, fmt.Errorf("instance %q of %q: app.file %q: %w",
			inst.InstName, inst.AppName, file, err)
	}

	return path, true, nil
}

// Check checks the syntax of file, a Lua file of the application of inst,
// with the tarantool the instance runs on: the syntax that counts is the
// one that tarantool accepts.
func Check(ctx context.Context, inst *InstanceCtx, file string) error {
	exe, err := resolveTarantool(inst)
	if err != nil {
		return err
	}

	var errBuff bytes.Buffer

	cmd := exec.CommandContext(ctx, exe.Path, "-e", checkSyntax)

	cmd.Env = append(os.Environ(), "TT_CLI_INSTANCE="+file)
	cmd.Dir = inst.AppDir
	cmd.Stderr = &errBuff

	err = cmd.Run()
	if err != nil {
		return syntaxCheckError(errBuff.String())
	}

	return nil
}

// GetAppInstanceName returns the target that names the instance,
// <app>:<instance>.
func GetAppInstanceName(instance InstanceCtx) string {
	return instance.AppName + string(InstanceDelimiter) + instance.InstName
}

// IsAbleToStartInstances checks if it is possible to start instances: each
// passes the checks before a start and its application has a tarantool,
// whose warnings are logged once per application. An external process needs
// no tarantool, only its binary (checkExternal).
//
// The command that starts the instances calls it before it detaches their
// watchdogs, so that what stops an instance from starting reaches the user
// rather than only the log of the watchdog.
func IsAbleToStartInstances(instances []InstanceCtx) (bool, error) {
	warned := map[string]bool{}

	for i := range instances {
		inst := &instances[i]

		err := checkStart(inst)
		if err != nil {
			return false, err
		}

		if IsExternal(inst) {
			continue
		}

		exe, err := resolveTarantool(inst)
		if err != nil {
			return false, err
		}

		if !warned[inst.AppDir] {
			for _, warning := range exe.Warnings {
				log.Warnf("%s: %s", inst.AppName, warning)
			}

			warned[inst.AppDir] = true
		}
	}

	return true, nil
}

// watchdogArgs returns the arguments of tt start that run the watchdog of
// instance: its target, and the scope, the application directory and the
// template it was resolved with, so that the watchdog runs the instance from
// the same application directory (PinnedOptions). Resolving the target anew
// in the watchdog could find another version, should <root>/apps/<app> move
// before the watchdog comes up.
func watchdogArgs(instance *InstanceCtx) []string {
	args := []string{
		"start", "--" + WatchdogFlag,
		"--" + ScopeFlag, instance.Scope.String(),
		"--" + AppDirFlag, instance.AppDir,
	}

	if instance.Template != "" {
		args = append(args, "--"+TemplateFlag, instance.Template)
	}

	return append(args, GetAppInstanceName(*instance))
}

// StartWatchdog starts the watchdog of instance in the background, a tt
// start --watchdog detached from the command, unless the instance is already
// running. args go before the arguments that run the watchdog.
//
// The watchdog gets no tt.yaml: it finds the application from what it is
// given (watchdogArgs).
func StartWatchdog(cmdCtx *cmdcontext.CmdCtx, ttExecutable string, instance InstanceCtx,
	args []string,
) error {
	appName := GetAppInstanceName(instance)
	// If an instance is already running don't try to start it again.
	// To restart an instance use tt restart command.
	procStatus := process_utils.ProcessStatus(instance.PIDFile)
	if procStatus.Code == process_utils.ProcStateRunning.Code {
		log.Infof("The instance %s (PID = %d) is already running.", appName, procStatus.PID)

		return nil
	}

	newArgs := []string{}
	if cmdCtx.Cli.IntegrityCheck != "" {
		newArgs = append(newArgs, "--integrity-check", cmdCtx.Cli.IntegrityCheck)
	}

	newArgs = append(newArgs, args...)
	newArgs = append(newArgs, watchdogArgs(&instance)...)

	f, err := cmdCtx.Integrity.Repository.Read(ttExecutable)
	if err != nil {
		return err
	}

	_ = f.Close()

	log.Infof("Starting an instance [%s]...", appName)

	// The watchdog leads a process group of its own, so it is not killed when
	// the session is closed, and tt kill kills it with its tarantool.
	_, err = supervisor.Detach(ttExecutable, newArgs, nil)
	if err != nil {
		return fmt.Errorf("cannot start the watchdog for %s: %w", appName, err)
	}

	return nil
}
