package running

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/cli/tarantool"
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
// instance runs from the config of the pair of its application
// (appClusterSpec) or from its script (appScriptSpec) on the tarantool of its
// application, an external process runs its binary (externalSpec). The stop
// settings of the instance record apply on top.
func instanceSpec(inst *InstanceCtx, opts specOptions) (supervisor.Spec, error) {
	build := appClusterSpec

	switch {
	case IsExternal(inst):
		build = externalSpec
	case IsScript(inst):
		build = appScriptSpec
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
// as it was sent, as it always has: SIGINT from tt instance stop, SIGQUIT
// from tt instance kill --dump. A record that names a signal is the authority
// on how its instance stops, as KillSignal= is for a systemd unit, so that
// signal is sent whatever stopped the watchdog, and nothing is forwarded.
func applyStopSettings(spec *supervisor.Spec, inst *InstanceCtx) {
	if inst.StopSignal != 0 {
		spec.StopSignal = inst.StopSignal
		spec.ForwardStopSignal = false
	}

	if inst.StopTimeout > 0 {
		spec.StopTimeout = inst.StopTimeout
	}
}

// stopWait is how long tt instance stop waits for the watchdog of inst to
// exit: the time tarantool may take to stop, plus the margin process_utils
// keeps over instanceStopTimeout. Waiting less for an instance whose record
// allows it longer would report a failure while the instance is still
// stopping.
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
		// tt instance stop, tt instance kill --dump and a SIGTERM reach
		// tarantool as they were sent.
		StopSignal:        syscall.SIGINT,
		ForwardStopSignal: true,
		StopTimeout:       instanceStopTimeout,
		Stdout:            opts.stdout,
		Stderr:            opts.stderr,
		// tarantool stays in the group of its watchdog, so that
		// tt instance kill kills both.
		ProcessGroup: false,
		// tt instance logrotate reaches tarantool as SIGHUP, which reopens
		// its log.
		ReloadSignal:        0,
		ForwardReloadSignal: true,
	}
}

// scriptSpec is the Spec of an instance that runs its script on the
// tarantool at tarantoolPath: tarantool reads the launcher from its standard
// input, and the launcher binds the console socket and runs the script, which
// configures the instance with box.cfg. tarantool runs in the application
// directory, as an instance configured by the cluster config does.
//
// The paths of the layout reach box.cfg as tt2 passed them: as the TT_
// variables of its options, TT_WAL_DIR, TT_MEMTX_DIR, TT_VINYL_DIR and
// TT_LISTEN, which box.cfg takes for the options the script leaves out,
// while an option the script passes wins. TT_LISTEN is left to the
// environment of tt when it has one, as tt2 left it. The launcher binds the
// console socket from its directory, so its path may be of any length. The
// log goes to the standard error, which the watchdog writes into tt.log;
// TARANTOOL_LOG is emptied for cartridge, which would log elsewhere.
// TARANTOOL_WORKDIR, TARANTOOL_APP_NAME and TARANTOOL_INSTANCE_NAME are
// there for cartridge as well; TARANTOOL_CFG, which pointed to instances.yml
// of tt2, is not, and the record sets it if the script needs it.
//
// The env of the instance record goes on top of the inherited environment
// and of those TT_ variables: a record configures its instance through them,
// as box.cfg would take them from any environment, and NewInstanceCtx
// follows the data directories it moves, as it does for a Tarantool instance
// configured by the cluster config. The variables of tt and of the launcher
// go on top of the record: it cannot move them under its own instance.
func scriptSpec(tarantoolPath string, inst *InstanceCtx, opts specOptions) (
	supervisor.Spec, error,
) {
	listen, err := listenURI(inst)
	if err != nil {
		return supervisor.Spec{}, err
	}

	args := []string{}
	if opts.integrity != nil {
		args = append(args, "--integrity-check",
			filepath.Join(inst.AppDir, integrity.HashesFileName))
	}

	args = append(args, "-")

	env := os.Environ()

	for _, dir := range [...]struct{ name, path string }{
		{"TT_WAL_DIR", inst.WalDir},
		{"TT_MEMTX_DIR", inst.MemtxDir},
		{"TT_VINYL_DIR", inst.VinylDir},
	} {
		if dir.path != "" {
			env = append(env, dir.name+"="+dir.path)
		}
	}

	_, listenSet := os.LookupEnv("TT_LISTEN")
	if listen != "" && !listenSet {
		env = append(env, "TT_LISTEN="+listen)
	}

	for _, name := range slices.Sorted(maps.Keys(inst.Env)) {
		env = append(env, name+"="+inst.Env[name])
	}

	env = append(env,
		"PWD="+inst.AppDir,
		"TT_CLI=true",
		"TT_CLI_INSTANCE="+inst.InstanceScript,
		"TT_CLI_WORK_DIR="+inst.AppDir,
		"TARANTOOL_WORKDIR="+inst.WalDir,
		"TARANTOOL_APP_NAME="+inst.AppName,
		"TARANTOOL_INSTANCE_NAME="+inst.InstName)

	if inst.ConsoleSocket != "" {
		env = append(env,
			"TT_CLI_CONSOLE_SOCKET=unix/:./"+filepath.Base(inst.ConsoleSocket),
			"TT_CLI_CONSOLE_SOCKET_DIR="+filepath.Dir(inst.ConsoleSocket))
	}

	if inst.LogDir != "" {
		env = append(env, "TARANTOOL_LOG=")
	}

	spec := tarantoolSpec(tarantoolPath, args, env, inst.AppDir, opts)

	spec.Stdin = instanceLauncher

	return spec, nil
}

// appScriptSpec is the Spec of an instance built from the application model
// (NewInstanceCtx) that runs its script, on the tarantool of its application
// (scriptSpec). Like appClusterSpec, it checks the instance at every start,
// since a restart runs with a record that may have changed since the first
// one.
func appScriptSpec(inst *InstanceCtx, opts specOptions) (supervisor.Spec, error) {
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

	return scriptSpec(exe.Path, inst, opts)
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

	// Quiver creates its data directory even when no space uses the engine.
	// CE rejects this option, so only give the default to EE instances.
	// Inspect the binary: a package with a CE platform requirement can bundle
	// EE and install it under versions/tarantool rather than tarantool-ee.
	if tarantool.DetectFlavor(context.Background(), exe.Path) == tarantool.FlavorEE {
		scopeLayout, err := layout.NewScopeLayout(inst.Root, inst.AppName, inst.InstName)
		if err != nil {
			return supervisor.Spec{}, err
		}

		env = append(env, "TT_QUIVER_DIR_DEFAULT="+scopeLayout.DataDir())
	}

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
