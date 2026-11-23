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
	errTheInstanceIsNotRunningItMustBeStarted = errors.New(
		": the instance is not running, it must be started",
	)
	// errWatchdogStarting is the error for a pid file that a watchdog holds
	// without its pid yet (pidfile.Owner): it is starting, and is not to be
	// signalled by a pid it has not written.
	errWatchdogStarting = errors.New("the watchdog of the instance is starting; try again")
)

const (
	defaultDirPerms = 0o770
	// killCleanupTimeout bounds the wait for the kernel to drop the pid file
	// lock of a killed watchdog.
	killCleanupTimeout = 5 * time.Second
)

// watchdogRestartTimeout is the pause of the watchdog between an exit of the
// instance and its restart. A test of a restart through Start sets a shorter
// one: the pause is not what it checks.
var watchdogRestartTimeout = 5 * time.Second

// WatchdogFlag is the hidden flag of tt instance start that makes it the
// watchdog of one instance (StartWatchdog), which it runs from the launch
// LaunchFlag names.
const WatchdogFlag = "watchdog"

// ScopeFlag is the flag of the scope of an application.
const ScopeFlag = sdk.ScopeFlag

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
	// Nil when no cluster config is loaded, and in the launch of a watchdog,
	// which keeps what tt takes from it, the paths of the instance (Launch).
	Configuration *goconfig.Config `json:"-"`
	// Scope is the scope of the application.
	Scope app.Scope
	// Root is the root of that scope, which the runtime artifacts of the
	// instance are under (layout.ScopeLayout).
	Root string
	// Template is the --template value the instance was resolved with,
	// empty for none.
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
	// tarantool, its script, its binary, the tt of its watchdog. The
	// watchdog finds them anew from the launch (LoadLaunch).
	Integrity *integrity.IntegrityCtx `json:"-"`
	// TCM is what the record of TCM says and what tt makes of it as TCM
	// starts (IsTCM); zero for any other instance.
	TCM TCMCtx
	// NotInConfig tells that the instance is an orphan (app.App.NotInConfig):
	// its watchdog runs, while the topology of the config no longer has it.
	// Its paths are those its watchdog runs it with (RunningLaunch).
	NotInConfig bool `json:"-"`

	// configVars are the variables tarantool substitutes into the config of
	// a Tarantool instance the cluster config configures, such as
	// {{ instance_name }} (configVars); nil for any other instance.
	configVars map[string]string
	// clusterConfig is the cluster config of the application of a Tarantool
	// instance the cluster config configures, as the application read it,
	// for ValidateConfigs; nil for any other instance and when it was not
	// read.
	clusterConfig *goconfig.MutableConfig
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

// removeLeftovers removes what a watchdog that died without cleaning up left
// besides its pid file, while the caller holds the lock of that file, so
// that no newer watchdog owns any of it (pidfile.RemoveFor): the sockets of
// a Tarantool instance, the state the watchdog published
// (watchdogStateFileName), and the pid file of the process of an external
// process. That file stays while it names a live process that leads its
// group, the process running without its watchdog (Orphan), which
// tt instance stop and kill stop.
func removeLeftovers(run *InstanceCtx) {
	removeSockets(run)
	removeWatchdogState(run)

	if run.ProcessPIDFile == "" {
		return
	}

	pid, alive, err := pidfile.Alive(run.ProcessPIDFile)
	if err != nil || alive && leadsGroup(pid) {
		return
	}

	_, err = pidfile.RemoveFor(run.ProcessPIDFile, pid, 0, nil)
	if err != nil {
		log.Warnf("cannot remove the pid file %q: %s", run.ProcessPIDFile, err)
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
// The watchdog starts the instance from launch, which tt instance start
// resolved (LoadLaunch), every time: a restart after an exit runs what the
// first start ran, on the version it started from, whatever the pair says
// by then. An explicit tt instance restart resolves the instance anew.
//
// report is the report file of the start that waits for the instance
// (ReportFlag), empty for none: the watchdog tells it how the first process
// it starts fares, or why it ends without one.
func Start(cmdCtx *cmdcontext.CmdCtx, launch *Launch, report string) error {
	rep := newReporter(report)

	_, err := superviseInstance(cmdCtx, launch, nil, rep)
	if err != nil {
		rep.end(err)
	}

	return err
}

// superviseInstance runs the watchdog of the instance of launch and returns
// how the supervision ended; the failure that ended it is in its log, and
// goes to rep, if there is one, as the events of the first start do. With
// echo it is the watchdog of tt instance start --foreground
// (StartForeground): it never restarts the instance, and copies its output
// to echo as well.
func superviseInstance(cmdCtx *cmdcontext.CmdCtx, launch *Launch, echo io.Writer,
	rep *reporter,
) (supervisionEnd, error) {
	inst := &launch.Instance
	exits := newExitRecorder(inst)

	err := createInstanceDataDirectories(*inst)
	if err != nil {
		err = fmt.Errorf("failed to create a directory: %w", err)
		exits.end(err)

		return supervisionEnd{}, err
	}

	logger, err := createLogger(inst)
	if err != nil {
		err = fmt.Errorf("cannot create a logger: %w", err)
		exits.end(err)

		return supervisionEnd{}, err
	}

	logger.Println("[INFO] Start") // Create a log file before any other actions.

	src := newInstanceSource(cmdCtx, launch, &watchdogLog{
		logger:      logger,
		checkPeriod: time.Duration(cmdCtx.Cli.IntegrityCheckPeriod) * time.Second,
	})

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
		exits.onEvent(event)
		// Reported once tt.log has it, which the report quotes.
		rep.onEvent(&src.ran, event)
	}

	engine, err := supervisor.New(source, opts)
	if err != nil {
		return supervisionEnd{}, fmt.Errorf("cannot create the watchdog: %w", err)
	}

	end.err = engine.Run(context.Background())
	src.log.onEnd(end.err)
	exits.end(end.err)
	rep.end(end.err)

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
// as the watchdog would have stopped it (stopOrphan). An instance whose pid
// file nobody holds is not running: the file is stale, and goes
// (interruptWatchdog). An instance that is not running is no failure, and
// is not told of: a restart starts it next.
func StopAllResults(runs []InstanceCtx) []error {
	return stopAll(runs, false)
}

// StopAllSaying is StopAllResults for tt instance stop, which says of every
// instance that is not running that it is not (LogNotRunning), as
// tt instance kill and logrotate do.
func StopAllSaying(runs []InstanceCtx) []error {
	return stopAll(runs, true)
}

// LogNotRunning says that the instance name is not running, which is no
// failure of a command that stops it, kills it or rotates its logs.
func LogNotRunning(name string) {
	log.Infof("The instance %s is not running.", name)
}

// stopAll is StopAllResults, which says of the instances that are not
// running that they are not with say.
func stopAll(runs []InstanceCtx, say bool) []error {
	type instanceProcess struct {
		index int
		name  string
		pids  instancePIDs
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

		pids, stale, err := interruptWatchdog(&run)

		switch {
		case err != nil:
			stopErrors[index] = fmt.Errorf("failed to stop instance %s: %w", name, err)
		case pids.watchdog == 0 && say && !stale:
			// A stale pid file removed is told of as such.
			LogNotRunning(name)
		case pids.watchdog == 0:
			log.Debugf("The instance %s is already stopped", name)
		default:
			processes = append(processes, instanceProcess{
				index: index, name: name, pids: pids, wait: stopWait(&run),
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
		err := process_utils.WaitProcessTermination(proc.pids.watchdog, proc.wait)
		if err != nil {
			// The user may have to finish the stop by hand: both are named.
			stopErrors[proc.index] = fmt.Errorf("failed to stop instance %s%s: %w",
				proc.name, proc.pids.withWatchdog(), err)

			continue
		}

		log.Infof("The instance %s%s has been terminated.", proc.name, proc.pids)
	}

	return stopErrors
}

// interruptWatchdog sends SIGINT to the watchdog of run, the process that
// holds its pid file, and returns its pid with the pid of the process it
// runs, read before the signal (pidsOf); none when no watchdog holds the
// file. A file nobody holds is removed (removeStale): the pid in it names a
// watchdog that is gone, or an unrelated process by now, and gets no signal;
// stale tells that it was removed, which removeStale says.
func interruptWatchdog(run *InstanceCtx) (instancePIDs, bool, error) {
	var none instancePIDs

	pid, owned, err := pidfile.Owner(run.PIDFile)
	if err != nil {
		return none, false, fmt.Errorf("can't get pid of running process: %w", err)
	}

	switch {
	case !owned:
		removed, err := removeStale(run, pid)

		return none, removed, err
	case pid == 0:
		// A watchdog holds the file and has not written its pid yet.
		return none, false, errWatchdogStarting
	}

	pids := pidsOf(run, pid)

	err = process_utils.Interrupt(pid)
	if err != nil {
		return none, false, err
	}

	return pids, false, nil
}

// removeStale removes the pid file of run that nobody holds, while it still
// names pid, and with it what else the watchdog that died without cleaning
// up left (removeLeftovers), under the lock of the file (pidfile.RemoveFor).
// It returns whether it removed the file, and logs that it did: a missing
// one is nothing to remove. A file a watchdog that has come up since holds
// is left, with an error wrapping pidfile.ErrBusy.
func removeStale(run *InstanceCtx, pid int) (bool, error) {
	removed, err := pidfile.RemoveFor(run.PIDFile, pid, 0, func() { removeLeftovers(run) })
	if err != nil {
		return false, fmt.Errorf("cannot remove the stale pid file %q: %w", run.PIDFile, err)
	}

	if removed {
		log.Infof("The instance %s is not running: removed the stale pid file %q.",
			GetAppInstanceName(*run), run.PIDFile)
	}

	return removed, nil
}

// IsStopped reports whether nothing of run runs: no watchdog holds its pid
// file, which is missing or stale (Status), and no process of an external
// process runs without its watchdog (Orphan). A pid file that cannot be
// read is not taken for stopped.
func IsStopped(run *InstanceCtx) bool {
	_, owned, err := pidfile.Owner(run.PIDFile)

	return err == nil && !owned && Orphan(run) == 0
}

// SettleStopped reports whether nothing of run runs, as IsStopped does, and
// if so removes a stale pid file and what else the watchdog that died
// without cleaning up left (removeStale). It is for a command that acts on
// the files of a stopped instance, such as tt instance clean.
func SettleStopped(run *InstanceCtx) (bool, error) {
	pid, owned, err := pidfile.Owner(run.PIDFile)
	if err != nil {
		return false, fmt.Errorf("cannot check the pid file: %w", err)
	}

	if owned || Orphan(run) != 0 {
		return false, nil
	}

	_, err = removeStale(run, pid)
	if err != nil {
		return false, err
	}

	return true, nil
}

// Kill kills instance process: the process group of its watchdog, and the
// process group of an external process, which is a group of its own, also
// when its watchdog is gone (killOrphan).
//
// Only the watchdog that holds the pid file is killed. An instance whose pid
// file is missing or nobody holds is not running, which is no failure:
// there is nothing to kill, and a stale file goes with what the watchdog
// that died left (removeStale). The pid in a stale file may name an
// unrelated process by now, whose group must not be killed.
func Kill(run InstanceCtx) error {
	orphan := Orphan(&run)
	if orphan != 0 {
		return killOrphan(&run, orphan)
	}

	fullInstanceName := GetAppInstanceName(run)

	pid, owned, err := pidfile.Owner(run.PIDFile)
	if err != nil {
		return fmt.Errorf("failed to kill the processes: %w", err)
	}

	if !owned {
		removed, err := removeStale(&run, pid)
		if err != nil {
			return fmt.Errorf("failed to kill the processes: %w", err)
		}

		if !removed {
			LogNotRunning(fullInstanceName)
		}

		return nil
	}

	if pid == 0 {
		return fmt.Errorf("failed to kill the processes: %w", errWatchdogStarting)
	}

	// The state goes with the watchdog.
	pids := pidsOf(&run, pid)

	err = process_utils.KillProcessGroup(pid)
	if err != nil {
		return fmt.Errorf("failed to kill the processes: %w", err)
	}

	killProcess(&run, killCleanupTimeout)
	cleanupAfterKill(&run, pid, killCleanupTimeout, func(run *InstanceCtx) {
		removeSockets(run)
		removeWatchdogState(run)
		recordKilled(run)
	})

	log.Infof("The instance %s%s has been killed.", fullInstanceName, pids)

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
// command reported one. So is an instance whose pid file no watchdog holds:
// it is not running, and there is nothing to take a dump of.
func Quit(run InstanceCtx) error {
	err := RequireTarantool(&run)
	if err != nil {
		return fmt.Errorf("cannot quit with a dump: %w", err)
	}

	pid, owned, err := pidfile.Owner(run.PIDFile)
	if err != nil {
		return fmt.Errorf("failed to quit the process: %w", err)
	}

	// The pid in a file nobody holds may name an unrelated process by now.
	switch {
	case !owned:
		return fmt.Errorf("cannot quit with a dump: %s%w", GetAppInstanceName(run),
			errTheInstanceIsNotRunningItMustBeStarted)
	case pid == 0:
		return fmt.Errorf("cannot quit with a dump: %w", errWatchdogStarting)
	}

	// The watchdog passes SIGQUIT on to tarantool, whose core file names it,
	// and removes its state as it ends.
	pids := pidsOf(&run, pid)

	err = process_utils.QuitProcess(pid)
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
	log.Infof("The instance %s%s has been terminated with dump.", fullInstanceName, pids)

	return nil
}

// Status returns the state of the watchdog of run, judged by its pid file
// (PIDFileStatus).
func Status(run *InstanceCtx) process_utils.ProcessState {
	return PIDFileStatus(run.PIDFile)
}

// PIDFileStatus returns the state of the watchdog whose pid file is path:
// RUNNING while a watchdog holds the file, which it does for its whole life
// (pidfile.Owner); NOT RUNNING without a file, or with one that holds no
// pid; and the dead state, with the pid in it, for a file nobody holds: the
// watchdog died without removing it, killed, crashed or gone with a reboot.
// The pid in such a file is not asked about, since it may name an unrelated
// process by now. A file that cannot be read counts as none.
func PIDFileStatus(path string) process_utils.ProcessState {
	return process_utils.OwnerStatus(path)
}

// Logrotate rotates logs of a started instance: its watchdog reopens tt.log
// on SIGHUP and passes SIGHUP on to tarantool, which reopens its own log, or
// sends an external process its logrotate_signal, if it has one. A process
// that does not handle the signal yet, having only just been started, does
// not get it, rather than dying of it (supervisor.ErrReloadNotHandled). Only the
// watchdog that holds the pid file gets the signal: the pid in a file nobody
// holds may name an unrelated process by now, and is an error. An instance
// with no pid file is not running, which is said and is no failure.
func Logrotate(run *InstanceCtx) error {
	fullInstanceName := GetAppInstanceName(*run)

	pid, owned, err := pidfile.Owner(run.PIDFile)

	switch {
	case err != nil:
		return fmt.Errorf("can't rotate logs: %w", err)
	case !owned && pid != 0:
		return errInstanceDead
	case !owned:
		// No log to reopen, which is no failure, as for tt instance stop
		// and kill.
		LogNotRunning(fullInstanceName)

		return nil
	case pid == 0:
		return fmt.Errorf("can't rotate logs: %w", errWatchdogStarting)
	}

	pids := pidsOf(run, pid)

	err = syscall.Kill(pid, syscall.SIGHUP)
	if err != nil {
		return fmt.Errorf(`can't rotate logs: "%w"`, err)
	}

	// Rotates logs [instance name pid].
	log.Infof("%s%s: logs has been rotated.", fullInstanceName, pids)

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

// OutputPrefix is the prefix of a line of the output of the instance name,
// <app>:<instance> (GetAppInstanceName), when the lines of several instances
// are shown together: tt instance logs and tt instance start --foreground
// prefix them alike, so that the lines of one read and grep the same.
func OutputPrefix(name string) string {
	return name + ": "
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

			if IsTCM(inst) {
				warning, _ := CheckTCMEdition(inst)
				if warning != "" {
					log.Warnf("%s", warning)
				}
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
// tt instance start --watchdog: the launch file it runs the instance from
// (LoadLaunch), which tt instance start resolved it to, and its target, which
// names it in the list of processes. The watchdog resolves nothing: the pair
// may have changed by the time it comes up, and <root>/apps/<app> may point
// to another version. A watchdog a start waits for gets the report file it
// tells that start about the instance through (ReportFlag). The watchdog of
// tt instance start --foreground gets --foreground as well.
func watchdogArgs(instance *InstanceCtx, files watchdogFiles, foreground bool) []string {
	args := append(slices.Clone(watchdogCommand),
		"--"+WatchdogFlag,
		"--"+LaunchFlag, files.launch,
	)

	if files.report != "" {
		args = append(args, "--"+ReportFlag, files.report)
	}

	if foreground {
		args = append(args, "--"+ForegroundFlag)
	}

	return append(args, GetAppInstanceName(*instance))
}

// watchdogFiles are the files the command that starts a watchdog hands it:
// the launch it runs, and the report file of a start that waits for it,
// empty for none.
type watchdogFiles struct {
	launch string
	report string
}

// watchdogCommandLine returns the arguments of ttExecutable that run the
// watchdog of instance with files: args, after --integrity-check when
// integrity checks are on, then watchdogArgs. The executable must pass the
// integrity checks.
func watchdogCommandLine(cmdCtx *cmdcontext.CmdCtx, ttExecutable string,
	instance *InstanceCtx, files watchdogFiles, args []string, foreground bool,
) ([]string, error) {
	cmdArgs := []string{}
	if cmdCtx.Cli.IntegrityCheck != "" {
		cmdArgs = append(cmdArgs, "--integrity-check", cmdCtx.Cli.IntegrityCheck)
	}

	cmdArgs = append(cmdArgs, args...)
	cmdArgs = append(cmdArgs, watchdogArgs(instance, files, foreground)...)

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

// clearForWatchdog readies the pid file of instance for a watchdog about to
// start: it reports whether a watchdog holds the file, the instance running
// already, with its pid, zero for one that is starting and has not written
// it yet; or removes a file nobody holds (removeStale), which Acquire in the
// new watchdog would refuse while the pid in it names an unrelated live
// process.
func clearForWatchdog(instance *InstanceCtx) (int, bool, error) {
	pid, owned, err := pidfile.Owner(instance.PIDFile)
	if err != nil {
		return 0, false, fmt.Errorf("cannot check the pid file: %w", err)
	}

	if owned {
		return pid, true, nil
	}

	_, err = removeStale(instance, pid)

	return 0, false, err
}

// StartWatchdog starts the watchdog of instance in the background, a
// tt instance start --watchdog detached from the command, unless the instance
// is already running: unless a watchdog holds its pid file. args go before
// the arguments that run the watchdog.
//
// The instance is resolved into its launch here (ResolveLaunch), which the
// watchdog runs for its whole life (watchdogArgs).
//
// It returns the watchdog it started, one that is not waited for
// (StartedWatchdog.Waits) for an instance that runs already. With wait the
// watchdog reports how its instance fares to the start, for WaitUp.
func StartWatchdog(cmdCtx *cmdcontext.CmdCtx, ttExecutable string, instance InstanceCtx,
	args []string, wait bool,
) (*StartedWatchdog, error) {
	appName := GetAppInstanceName(instance)
	// If an instance is already running don't try to start it again.
	// To restart an instance use tt instance restart command.
	pid, running, err := clearForWatchdog(&instance)
	if err != nil {
		return nil, fmt.Errorf("cannot start the watchdog for %s: %w", appName, err)
	}

	if running {
		log.Infof("The instance %s%s is already running.", appName, DescribePID(&instance, pid))

		return &StartedWatchdog{Instance: instance, exited: nil, report: ""}, nil
	}

	launchFile, err := prepareWatchdog(&instance)
	if err != nil {
		return nil, err
	}

	report := ""
	if wait {
		report, err = newReportFile(&instance)
		if err != nil {
			_ = os.Remove(launchFile)

			return nil, err
		}
	}

	cleanup := func() {
		_ = os.Remove(launchFile)

		if report != "" {
			_ = os.Remove(report)
		}
	}

	newArgs, err := watchdogCommandLine(cmdCtx, ttExecutable, &instance,
		watchdogFiles{launch: launchFile, report: report}, args, false)
	if err != nil {
		cleanup()

		return nil, err
	}

	log.Infof("Starting an instance [%s]...", appName)

	// The watchdog leads a process group of its own, so it is not killed when
	// the session is closed, and tt instance kill kills it with its tarantool.
	_, exited, err := supervisor.DetachWatched(ttExecutable, newArgs, nil)
	if err != nil {
		cleanup()

		return nil, fmt.Errorf("cannot start the watchdog for %s: %w", appName, err)
	}

	return &StartedWatchdog{Instance: instance, exited: exited, report: report}, nil
}

// prepareWatchdog resolves instance into its launch (ResolveLaunch) and
// writes it into the file its watchdog runs it from, whose path it returns.
//
// What the watchdog needs before it has a log to write to is made or checked
// here, by the command that starts it, which reports what fails: a watchdog
// in the background whose directories cannot be made, or whose tt.log cannot
// be written, would end with nowhere to say why, and a start that does not
// wait would succeed. The run directory is checked by the launch file
// written into it.
func prepareWatchdog(instance *InstanceCtx) (string, error) {
	launch, err := ResolveLaunch(instance)
	if err != nil {
		return "", err
	}

	name := GetAppInstanceName(*instance)

	err = createInstanceDataDirectories(*instance)
	if err != nil {
		return "", fmt.Errorf("instance %s: failed to create a directory: %w", name, err)
	}

	if instance.Log != "" {
		err = ttlog.CheckLogFile(instance.Log)
		if err != nil {
			return "", fmt.Errorf("instance %s: %w", name, err)
		}
	}

	launchFile, err := writeLaunchFile(&launch)
	if err != nil {
		return "", fmt.Errorf("instance %s: %w", name, err)
	}

	return launchFile, nil
}
