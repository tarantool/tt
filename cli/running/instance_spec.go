package running

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

var (
	errSocketPathIsLongerThanSymbols = errors.New("socket path is longer than ")
	errApplicationIsNotADirectory    = errors.New("application ")
)

const (
	maxSocketPathLinux = 108
	maxSocketPathMac   = 104
	// instanceStopTimeout is how long an instance may take to stop before it
	// is killed.
	instanceStopTimeout = 30 * time.Second
)

//go:embed lua/launcher.lua
var instanceLauncher []byte

// specOptions are the parts of the Spec of an instance that do not come from
// its context.
type specOptions struct {
	// integrity is the integrity context when integrity checks are on, nil
	// otherwise.
	integrity *integrity.IntegrityCtx
	stdout    io.Writer
	stderr    io.Writer
	// warn reports a diagnostic that does not stop the start, such as the
	// tarantool of a project differing from the one its lock bundles; nil
	// drops it.
	warn func(message string)
}

// warnf reports a diagnostic through warn, if there is one.
func (opts *specOptions) warnf(format string, args ...any) {
	if opts.warn != nil {
		opts.warn(fmt.Sprintf(format, args...))
	}
}

func integrityOf(cmdCtx *cmdcontext.CmdCtx) *integrity.IntegrityCtx {
	if cmdCtx.Cli.IntegrityCheck == "" {
		return nil
	}

	return &cmdCtx.Integrity
}

// instanceSpec is the Spec of the process of an instance: a Tarantool
// instance runs from the config of the pair of its application on the
// tarantool of its application (appClusterSpec), an external process runs
// its binary (externalSpec). The stop settings of the instance record apply
// on top.
func instanceSpec(inst *InstanceCtx, opts specOptions) (supervisor.Spec, error) {
	build := appClusterSpec
	if IsExternal(inst) {
		build = externalSpec
	}

	spec, err := build(inst, opts)
	if err != nil {
		return supervisor.Spec{}, err
	}

	applyStopSettings(&spec, inst)

	return spec, nil
}

// applyStopSettings applies the stop_signal and stop_timeout of the instance
// record to spec, over the defaults of tarantoolSpec or externalSpec.
//
// Without stop_signal the stop signal the watchdog receives reaches tarantool
// as it was sent, as it always has: SIGINT from tt stop, SIGQUIT from tt quit.
// A record that names a signal is the authority on how its instance stops,
// as KillSignal= is for a systemd unit, so that signal is sent whatever
// stopped the watchdog, and nothing is forwarded.
func applyStopSettings(spec *supervisor.Spec, inst *InstanceCtx) {
	if inst.StopSignal != 0 {
		spec.StopSignal = inst.StopSignal
		spec.ForwardStopSignal = false
	}

	if inst.StopTimeout > 0 {
		spec.StopTimeout = inst.StopTimeout
	}
}

// stopWait is how long tt stop waits for the watchdog of inst to exit: the
// time tarantool may take to stop, plus the margin process_utils keeps over
// instanceStopTimeout. Waiting less for an instance whose record allows it
// longer would report a failure while the instance is still stopping.
func stopWait(inst *InstanceCtx) time.Duration {
	if inst.StopTimeout <= instanceStopTimeout {
		return process_utils.TerminationTimeout
	}

	return inst.StopTimeout + process_utils.TerminationTimeout - instanceStopTimeout
}

// tarantoolSpec is what the Specs of both kinds of instances share: how
// tarantool runs, stops and reports.
func tarantoolSpec(tarantoolPath string, args, env []string, dir string,
	opts specOptions,
) supervisor.Spec {
	return supervisor.Spec{
		Path:  tarantoolPath,
		Args:  args,
		Env:   env,
		Dir:   dir,
		Stdin: nil,
		// tt stop, tt quit and a SIGTERM reach tarantool as they were sent.
		StopSignal:        syscall.SIGINT,
		ForwardStopSignal: true,
		StopTimeout:       instanceStopTimeout,
		Stdout:            opts.stdout,
		Stderr:            opts.stderr,
		// tarantool stays in the group of its watchdog, so that tt kill
		// kills both.
		ProcessGroup: false,
		// tt logrotate reaches tarantool as SIGHUP, which reopens its log.
		ReloadSignal:        0,
		ForwardReloadSignal: true,
	}
}

// scriptSpec is the Spec of an instance that runs its script: tarantool reads
// the launcher from its standard input, and the launcher runs the script.
//
// Nothing builds an instance that runs its script while NewInstanceCtx
// refuses the records with script; it is kept, with its launcher, for them.
func scriptSpec(tarantoolPath string, inst *InstanceCtx, opts specOptions) (
	supervisor.Spec, error,
) {
	_, err := exec.LookPath(tarantoolPath)
	if err != nil {
		return supervisor.Spec{}, fmt.Errorf("looking for the tarantool executable: %w", err)
	}

	_, err = os.Stat(inst.InstanceScript)
	if err != nil {
		return supervisor.Spec{}, fmt.Errorf("checking the instance script: %w", err)
	}

	args := []string{}

	if opts.integrity != nil {
		file, err := opts.integrity.Repository.Read(tarantoolPath)
		if err != nil {
			return supervisor.Spec{}, err
		}

		_ = file.Close()

		args = append(args, "--integrity-check", integrity.HashesFileName)
	}

	args = append(args, "-")

	workDir := inst.AppDir
	if workDir == "" {
		workDir = filepath.Dir(inst.InstanceScript)
	}

	if !util.IsDir(workDir) {
		err = os.MkdirAll(workDir, defaultDirPerms)
		if err != nil {
			return supervisor.Spec{}, fmt.Errorf("failed to create application directory %q: %w",
				workDir, err)
		}
	}

	env := append(os.Environ(), "TT_CLI_INSTANCE="+inst.InstanceScript, "PWD="+workDir)

	_, listenSet := os.LookupEnv("TT_LISTEN")
	if inst.BinaryPort != "" && !listenSet {
		env = append(env, "TT_LISTEN="+inst.BinaryPort)
	}

	if inst.ConsoleSocket != "" {
		consoleSocket, err := shortenSocketPath(inst.ConsoleSocket, workDir)
		if err != nil {
			return supervisor.Spec{}, err
		}

		env = append(env,
			"TT_CLI_CONSOLE_SOCKET="+"unix/:./"+filepath.Base(consoleSocket),
			"TT_CLI_CONSOLE_SOCKET_DIR="+filepath.Dir(consoleSocket))
	}

	env = append(env,
		"TT_CLI_INSTANCE="+inst.InstanceScript,
		"TT_CLI_WORK_DIR="+workDir,
		"TT_CLI=true",
		"TT_VINYL_DIR="+inst.VinylDir,
		"TT_WAL_DIR="+inst.WalDir,
		"TT_MEMTX_DIR="+inst.MemtxDir,
		"TARANTOOL_WORKDIR="+inst.WalDir,
		"TARANTOOL_APP_NAME="+inst.AppName,
		"TARANTOOL_INSTANCE_NAME="+inst.InstName)

	if inst.AppName != inst.InstName {
		env = append(env,
			"TARANTOOL_CFG="+filepath.Dir(inst.InstanceScript)+"/instances.yml")
	}

	if inst.LogDir != "" {
		env = append(env, "TARANTOOL_LOG=")
	}

	spec := tarantoolSpec(tarantoolPath, args, env, workDir, opts)

	spec.Stdin = instanceLauncher

	return spec, nil
}

// appClusterSpec is the Spec of an instance built from the application model
// (NewInstanceCtx): a Tarantool instance configured by the config of the pair
// of its application, on the tarantool of its application.
//
// It checks the instance at every start (checkStart), since a restart runs
// with a config that may have changed since the first one. tarantool runs in
// the application directory and gets the paths of the layout as the
// TT_*_DEFAULT variables, all absolute but a socket too long for that, which
// is given relative to the application directory where tarantool takes it
// so (socketArgs): the config overrides them, and a relative default would
// be resolved against a version directory, which is never written to.
// log.file is among them, so that an instance that logs to a file without
// naming one logs next to tt.log rather than into the application directory.
//
// The env of the instance record goes on top of the inherited environment,
// and the defaults above go on top of it: a record changes the config of its
// instance through the TT_ variables, which tarantool ranks above the config
// files and NewInstanceCtx reads, not by moving the layout under tt.
func appClusterSpec(inst *InstanceCtx, opts specOptions) (supervisor.Spec, error) {
	err := checkStart(inst)
	if err != nil {
		return supervisor.Spec{}, err
	}

	if !util.IsDir(inst.AppDir) {
		return supervisor.Spec{}, fmt.Errorf("%w%q is not a directory",
			errApplicationIsNotADirectory, inst.AppDir)
	}

	exe, err := resolveTarantool(inst)
	if err != nil {
		return supervisor.Spec{}, err
	}

	for _, warning := range exe.Warnings {
		opts.warnf("instance %q of %q: %s", inst.InstName, inst.AppName, warning)
	}

	console, listen, err := socketArgs(inst)
	if err != nil {
		return supervisor.Spec{}, err
	}

	args := []string{"-n", inst.InstName, "-c", inst.ClusterConfigPath}
	if opts.integrity != nil {
		args = append(args, "--integrity-check",
			filepath.Join(inst.AppDir, integrity.HashesFileName))
	}

	env := os.Environ()

	for _, name := range slices.Sorted(maps.Keys(inst.Env)) {
		env = append(env, name+"="+inst.Env[name])
	}

	env = append(env,
		"PWD="+inst.AppDir,
		"TT_WAL_DIR_DEFAULT="+inst.WalDir,
		"TT_SNAPSHOT_DIR_DEFAULT="+inst.MemtxDir,
		"TT_VINYL_DIR_DEFAULT="+inst.VinylDir,
		"TT_LOG_FILE_DEFAULT="+filepath.Join(inst.LogDir, layout.TarantoolLogFileName),
		"TT_PROCESS_PID_FILE_DEFAULT="+filepath.Join(inst.RunDir, layout.TarantoolPidFileName),
		"TT_CONSOLE_SOCKET_DEFAULT="+console,
		"TT_IPROTO_LISTEN_DEFAULT="+listen)

	return tarantoolSpec(exe.Path, args, env, inst.AppDir, opts), nil
}

// socketPathLimit is the size of sun_path, the room for the path in a unix
// socket address, which also holds the terminating NUL.
func socketPathLimit() int {
	if runtime.GOOS == "darwin" {
		return maxSocketPathMac
	}

	return maxSocketPathLinux
}

// verifySocketLength refuses a path that does not fit a unix socket address.
func verifySocketLength(socketPath string) error {
	maxSocketPath := socketPathLimit()

	if socketPath != "" && len(socketPath) >= maxSocketPath {
		return fmt.Errorf("%w%d symbols: %q",
			errSocketPathIsLongerThanSymbols, maxSocketPath-1, socketPath)
	}

	return nil
}

// shortenSocketPath returns socketPath, or, when it is too long for a unix
// socket, the path relative to basePath, the working directory of tarantool.
func shortenSocketPath(socketPath, basePath string) (string, error) {
	err := verifySocketLength(socketPath)
	if err == nil {
		return socketPath, nil
	}

	relativeSocketPath, err := filepath.Rel(basePath, socketPath)
	if err != nil {
		return "", fmt.Errorf("shortening the socket path %q: %w", socketPath, err)
	}

	err = verifySocketLength(relativeSocketPath)
	if err != nil {
		return "", err
	}

	return relativeSocketPath, nil
}
