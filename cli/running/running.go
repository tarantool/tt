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
	"slices"
	"syscall"
	"time"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/integrity"
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
	watchdogRestartTimeout = 5 * time.Second
	// killCleanupTimeout bounds the wait for the kernel to drop the pid file
	// lock of a killed watchdog.
	killCleanupTimeout = 5 * time.Second
)

// The flags of tt instance start that make it the watchdog of one instance
// (StartWatchdog), and pin the application the watchdog runs the instance
// from: the watchdog must find the same application directory that the
// command that started it found, not whatever <root>/apps/<app> points to by
// the time it comes up. --watchdog and --app-dir are hidden; --scope and
// --template are the flags every command of tt instance takes.
const (
	// WatchdogFlag makes tt instance start run the watchdog of its one
	// target.
	WatchdogFlag = "watchdog"
	// ScopeFlag is the scope of the application.
	ScopeFlag = sdk.ScopeFlag
	// TemplateFlag is the template the instance was resolved with.
	TemplateFlag = sdk.TemplateFlag
	// AppDirFlag pins the application directory (app.Options.Dir).
	AppDirFlag = "app-dir"
)

// watchdogCommand is the command that runs the watchdog of an instance:
// tt instance start, with WatchdogFlag.
var watchdogCommand = []string{"instance", "start"}

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
	// itself (IsScript), an absolute path, which scriptSpec runs; empty for
	// the other kinds.
	InstanceScript string
	// Kind tells how the instance runs: a Tarantool instance configured by
	// the cluster config or by its script (IsScript), or an external process
	// (IsExternal).
	Kind instances.Kind
	// Binary is the executable of an external process: an absolute path, or
	// the name of a program looked up when the process starts, in .rocks/bin
	// of the application and then in PATH (ResolveBinary).
	Binary string
	// Args are the arguments of an external process.
	Args []string
	// Workdir is the working directory of an external process: workdir of
	// its record, or else DataDir.
	Workdir string
	// DataDir is the data directory of an external process that runs in it,
	// one whose record sets no workdir: <root>/var/lib/<app>/<inst>/, which
	// the start creates and tt instance clean empties. It is empty for a
	// process with workdir and for a Tarantool instance, whose data
	// directories are WalDir, MemtxDir and VinylDir.
	DataDir string
	// LogrotateSignal is the signal tt instance logrotate sends an external
	// process after reopening tt.log; zero sends it none.
	LogrotateSignal syscall.Signal
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
	// SIGINT from tt instance stop, and stops an external process with SIGTERM.
	StopSignal syscall.Signal
	// StopTimeout is how long the instance record lets the instance take to
	// stop before it is killed. Zero is instanceStopTimeout.
	StopTimeout time.Duration
	// Control UNIX socket for started instance.
	ConsoleSocket string
	// Unix socket used as "binary port".
	BinaryPort string
	// ClusterConfigPath is the cluster config of the pair of the
	// application, the file tarantool -c gets; empty for an instance the
	// cluster config does not configure.
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
	// Unit is the systemd unit that manages the instance, as the mark of the
	// units tt service systemd install wrote says, or empty. AttachUnits sets
	// it; NewInstanceCtx leaves it empty.
	Unit string
	// Integrity are the integrity checks of the application of the instance
	// and of the runtimes it runs on (AppIntegrity), nil when they are off.
	// What the instance runs is checked with them before it is run: its
	// tarantool, its script, its binary, the tt of its watchdog.
	Integrity *integrity.IntegrityCtx

	// integrityFor finds the checks again where the watchdog reloads the
	// instance (PinnedOptions): app.Options.IntegrityFor it was found with.
	integrityFor func(app.Scope, string) (integrity.IntegrityCtx, error)

	// configVars are the variables tarantool substitutes into the config of
	// a Tarantool instance the cluster config configures, such as
	// {{ instance_name }} (configVars); nil for any other instance.
	configVars map[string]string
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

// renderInstCtxMembers substitutes the variables of the config, such as
// {{ instance_name }}, into the paths of the instance context (configVars;
// instance_name alone for an instance the cluster config does not
// configure). A variable tarantool would not know is an error.
func renderInstCtxMembers(instance *InstanceCtx) error {
	templateData := instance.configVars
	if templateData == nil {
		templateData = map[string]string{"instance_name": instance.InstName}
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

// Start runs the watchdog of an instance, the process tt instance start
// detaches with --watchdog, until the supervision is over. The pid file of
// the instance names the watchdog, so tt instance stop, status, kill and
// logrotate reach it; watchdogOptions says what it does with their signals.
//
// Before every start the watchdog reads the instance anew from the pair of
// the application directory it started from (reload): a restart after an
// exit picks up an edited config and restart policy, and stays on the
// version it started from. ctx bounds those reads, which may go to a config
// storage.
func Start(ctx context.Context, cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx) error {
	pinned := *inst

	//nolint:contextcheck // The watchdog runs until it is stopped; ctx bounds the reads.
	_, err := superviseInstance(cmdCtx, inst, func() (InstanceCtx, error) {
		return reload(ctx, &pinned, cmdCtx.Integrity)
	}, nil)

	return err
}

// superviseInstance runs the watchdog of inst, which takes the context of
// the instance from refresh before every start and after every exit, and
// returns how the supervision ended; the failure that ended it is in its
// log. With echo it is the watchdog of tt instance start --foreground
// (StartForeground): it never restarts the instance, and copies its output
// to echo as well.
func superviseInstance(cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx,
	refresh func() (InstanceCtx, error), echo io.Writer,
) (supervisionEnd, error) {
	err := checkStart(inst)
	if err != nil {
		return supervisionEnd{}, err
	}

	err = createInstanceDataDirectories(*inst)
	if err != nil {
		return supervisionEnd{}, fmt.Errorf("failed to create a directory: %w", err)
	}

	err = linkIntegrityLayout(inst)
	if err != nil {
		return supervisionEnd{}, err
	}

	logger, err := createLogger(inst)
	if err != nil {
		return supervisionEnd{}, fmt.Errorf("cannot create a logger: %w", err)
	}

	logger.Println("[INFO] Start") // Create a log file before any other actions.

	src := newInstanceSource(cmdCtx, inst, &watchdogLog{
		logger:      logger,
		checkPeriod: time.Duration(cmdCtx.Cli.IntegrityCheckPeriod) * time.Second,
	}, refresh)

	var source supervisor.Source = src

	if echo != nil {
		src.stdout = echoWriter{log: src.log, echo: echo}
		src.stderr = echoWriter{log: src.log, echo: echo}
		source = foregroundSource{src}
	}

	var end supervisionEnd

	opts := watchdogOptions(cmdCtx, src)
	onEvent := opts.OnEvent

	opts.OnEvent = func(event supervisor.Event) {
		end.record(event)
		onEvent(event)
	}

	engine, err := supervisor.New(source, opts)
	if err != nil {
		return supervisionEnd{}, fmt.Errorf("cannot create the watchdog: %w", err)
	}

	end.err = engine.Run(context.Background())
	src.log.onEnd(end.err)

	return end, nil
}

// Stop stops an instance and reports any interruption or termination error.
func Stop(run *InstanceCtx) error {
	return StopAll([]InstanceCtx{*run})
}

// StopAll interrupts all selected instances before waiting for them to exit.
// A missing pid file means the instance is already stopped. Other errors are
// collected without preventing the remaining instances from stopping.
func StopAll(runs []InstanceCtx) error {
	return errors.Join(StopAllResults(runs)...)
}

// StopAllResults stops the batch and returns one error per input instance, in
// order. The process of an external process whose watchdog is gone is stopped
// as the watchdog would have stopped it (stopOrphan).
func StopAllResults(runs []InstanceCtx) []error {
	type instanceProcess struct {
		index int
		name  string
		pid   int
		wait  time.Duration
	}

	type orphanProcess struct {
		index int
		run   InstanceCtx
		pid   int
	}

	processes := make([]instanceProcess, 0, len(runs))
	stopErrors := make([]error, len(runs))

	var orphans []orphanProcess

	for index, run := range runs {
		orphan := Orphan(&run)
		if orphan != 0 {
			orphans = append(orphans, orphanProcess{index: index, run: run, pid: orphan})

			continue
		}

		name := GetAppInstanceName(run)

		pid, err := process_utils.InterruptProcess(run.PIDFile)

		switch {
		case errors.Is(err, fs.ErrNotExist):
			log.Debugf("The instance %s is already stopped", name)
		case err != nil:
			stopErrors[index] = fmt.Errorf("failed to stop instance %s: %w", name, err)
		default:
			processes = append(processes, instanceProcess{
				index: index, name: name, pid: pid, wait: stopWait(&run),
			})
		}
	}

	for _, orphan := range orphans {
		err := stopOrphan(&orphan.run, orphan.pid)
		if err != nil {
			stopErrors[orphan.index] = err
		}
	}

	for _, proc := range processes {
		err := process_utils.WaitProcessTermination(proc.pid, proc.wait)
		if err != nil {
			stopErrors[proc.index] = fmt.Errorf("failed to stop instance %s (PID = %d): %w",
				proc.name, proc.pid, err)

			continue
		}

		log.Infof("The Instance %s (PID = %v) has been terminated.", proc.name, proc.pid)
	}

	return stopErrors
}

// Kill kills instance process: the process group of its watchdog, and the
// process group of an external process, which is a group of its own, also
// when its watchdog is gone (killOrphan).
func Kill(run InstanceCtx) error {
	orphan := Orphan(&run)
	if orphan != 0 {
		return killOrphan(&run, orphan)
	}

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

// cleanupAfterKill cleans up after the watchdog pid that tt instance kill
// killed and that cannot clean up after itself. Its pid file goes only while
// it still names pid, once the kernel has dropped the lock, which may take up
// to wait; removeSockets runs just before, under that lock. With no pid file
// at all, the sockets go under a pid file tt instance kill takes for their
// removal. Either way no watchdog owns the instance while its sockets are
// removed: one that comes up in the meantime keeps its pid file and its
// sockets.
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

// Logrotate rotates logs of a started instance: its watchdog reopens tt.log
// on SIGHUP and passes SIGHUP on to tarantool, which reopens its own log, or
// sends an external process its logrotate_signal, if it has one.
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

// AppFile returns the application file of inst, the Lua file it runs: the
// script of an instance that runs one, or app.file of its effective config,
// resolved against its application directory, which is what tarantool
// resolves it against. found is false when the config names none.
func AppFile(inst *InstanceCtx) (string, bool, error) {
	if IsScript(inst) {
		return inst.InstanceScript, true, nil
	}

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
	//nolint:contextcheck // The scope roots ask uname and take no context.
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
// whose warnings are logged once per application, and which can run it
// (checkConfigStorage). An external process needs no tarantool, only its
// binary (checkExternal), and must not run already without its watchdog
// (Orphan): a second copy would start next to it.
//
// The command that starts the instances calls it before it detaches their
// watchdogs, so that what stops an instance from starting reaches the user
// rather than only the log of the watchdog.
func IsAbleToStartInstances(instances []InstanceCtx) (bool, error) {
	warned := map[string]bool{}
	// flavors are the flavors of the tarantools asked so far, by path: a
	// tarantool from PATH is run to tell, once.
	flavors := map[string]string{}

	for i := range instances {
		inst := &instances[i]

		err := checkStart(inst)
		if err != nil {
			return false, err
		}

		if IsExternal(inst) {
			err = checkOrphan(inst)
			if err != nil {
				return false, err
			}

			continue
		}

		err = linkIntegrityLayout(inst)
		if err != nil {
			return false, err
		}

		exe, err := resolveTarantool(inst)
		if err != nil {
			return false, err
		}

		err = checkSignedTarantool(inst, exe)
		if err != nil {
			return false, err
		}

		if !warned[inst.AppDir] {
			for _, warning := range exe.Warnings {
				log.Warnf("%s: %s", inst.AppName, warning)
			}

			warned[inst.AppDir] = true
		}

		if _, stored := storageReference(inst); !stored {
			continue
		}

		flavor, known := flavors[exe.Path]
		if !known {
			flavor = exe.Flavor(context.Background())
			flavors[exe.Path] = flavor
		}

		err = checkConfigStorage(inst, exe, flavor)
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

// watchdogArgs returns the arguments of tt that run the watchdog of instance,
// tt instance start --watchdog: its target, and the scope, the application
// directory and the template it was resolved with, so that the watchdog runs
// the instance from the same application directory (PinnedOptions).
// Resolving the target anew in the watchdog could find another version,
// should <root>/apps/<app> move before the watchdog comes up. The watchdog
// of tt instance start --foreground gets --foreground as well.
func watchdogArgs(instance *InstanceCtx, foreground bool) []string {
	args := append(slices.Clone(watchdogCommand),
		"--"+WatchdogFlag,
		"--"+ScopeFlag, instance.Scope.String(),
		"--"+AppDirFlag, instance.AppDir,
	)

	if instance.Template != "" {
		args = append(args, "--"+TemplateFlag, instance.Template)
	}

	if foreground {
		args = append(args, "--"+ForegroundFlag)
	}

	return append(args, GetAppInstanceName(*instance))
}

// watchdogCommandLine returns the arguments of ttExecutable that run the
// watchdog of instance: args, after --integrity-check when integrity checks
// are on, then watchdogArgs. The executable must pass the integrity checks.
func watchdogCommandLine(cmdCtx *cmdcontext.CmdCtx, ttExecutable string,
	instance *InstanceCtx, args []string, foreground bool,
) ([]string, error) {
	cmdArgs := []string{}
	if cmdCtx.Cli.IntegrityCheck != "" {
		cmdArgs = append(cmdArgs, "--integrity-check", cmdCtx.Cli.IntegrityCheck)
	}

	cmdArgs = append(cmdArgs, args...)
	cmdArgs = append(cmdArgs, watchdogArgs(instance, foreground)...)

	repository := cmdCtx.Integrity.Repository
	if instance.Integrity != nil {
		repository = instance.Integrity.Repository
	}

	reader, err := repository.Read(ttExecutable)
	if err != nil {
		if instance.Integrity != nil {
			return nil, fmt.Errorf("the watchdog of %s: tt %s %w by the package or the "+
				"runtimes it runs on: %w", GetAppInstanceName(*instance), ttExecutable,
				errNotSigned, err)
		}

		return nil, err
	}

	_ = reader.Close()

	return cmdArgs, nil
}

// StartWatchdog starts the watchdog of instance in the background, a
// tt instance start --watchdog detached from the command, unless the instance
// is already running. args go before the arguments that run the watchdog.
//
// The watchdog gets no tt.yaml: it finds the application from what it is
// given (watchdogArgs).
func StartWatchdog(cmdCtx *cmdcontext.CmdCtx, ttExecutable string, instance InstanceCtx,
	args []string,
) error {
	appName := GetAppInstanceName(instance)
	// If an instance is already running don't try to start it again.
	// To restart an instance use tt instance restart command.
	procStatus := process_utils.ProcessStatus(instance.PIDFile)
	if procStatus.Code == process_utils.ProcStateRunning.Code {
		log.Infof("The instance %s (PID = %d) is already running.", appName, procStatus.PID)

		return nil
	}

	newArgs, err := watchdogCommandLine(cmdCtx, ttExecutable, &instance, args, false)
	if err != nil {
		return err
	}

	log.Infof("Starting an instance [%s]...", appName)

	// The watchdog leads a process group of its own, so it is not killed when
	// the session is closed, and tt instance kill kills it with its tarantool.
	_, err = supervisor.Detach(ttExecutable, newArgs, nil)
	if err != nil {
		return fmt.Errorf("cannot start the watchdog for %s: %w", appName, err)
	}

	return nil
}
