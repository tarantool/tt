package running

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/pidfile"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// The watchdog of an instance runs what tt instance start resolved, not what
// the pair says by the time the watchdog comes up or restarts the instance:
// tt instance start resolves the instance, its binary, its arguments, its
// environment, its restart policy and its paths, into a Launch before it
// returns, hands it to the watchdog in a file (LaunchFlag), and the watchdog
// starts the instance from it for its whole life, restarts included. An edit
// of instances.yaml, or of what tt takes from config.yaml, the paths of the
// instance, applies from the next explicit start or tt instance restart on.
// tarantool reads config.yaml itself at every start, as config:reload()
// does, so an edit of the cluster config reaches a restarted instance all
// the same.
//
// The watchdog keeps the launch it runs in launchFileName in the run
// directory of the instance, for tt instance status --details, while it
// holds the pid file of the instance.

// LaunchFlag is the hidden flag of tt instance start --watchdog that names
// the file of the Launch the watchdog runs (StartWatchdog).
const LaunchFlag = "launch"

const (
	// launchFileName is the file in the run directory of an instance that
	// holds the launch its watchdog runs, while the watchdog runs.
	launchFileName = "launch.json"
	// launchVersion is the version of the format of a Launch: a watchdog
	// refuses a launch of another.
	launchVersion = 1
	// launchFileMode is the mode of a launch file, which holds the
	// environment the record gives the instance.
	launchFileMode = 0o600
)

// errLaunch is wrapped by the error for a launch file that cannot be read.
var errLaunch = errors.New("cannot read the launch of the instance")

// Launch is an instance as tt instance start resolved it: the context of the
// instance, the process its watchdog runs, and the tarantool it runs on.
type Launch struct {
	// Version is launchVersion.
	Version int `json:"version"`
	// Instance is the context of the instance.
	Instance InstanceCtx `json:"instance"`
	// Spec is the process the watchdog runs.
	Spec LaunchSpec `json:"spec"`
	// Tarantool names the tarantool a Tarantool instance runs on and where
	// it comes from, as DescribeTarantool names it at the start, with the
	// warnings of the choice; empty for an external process.
	Tarantool         string   `json:"tarantool,omitempty"`
	TarantoolWarnings []string `json:"tarantool_warnings,omitempty"`
	// Watchdog is the pid of the watchdog that runs the launch, in the
	// launch file of the run directory (launchFileName); zero in the file
	// tt instance start hands the watchdog.
	Watchdog int `json:"watchdog,omitempty"`
}

// LaunchSpec is the supervisor.Spec of the process of a Launch, without the
// writers its output goes to, which are the watchdog's.
type LaunchSpec struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
	// Env is the environment of the process. With InheritEnv it goes on top
	// of the environment of the watchdog, which it inherits from
	// tt instance start: the launch file keeps what tt and the record add,
	// not the whole environment of the user.
	Env        []string `json:"env"`
	InheritEnv bool     `json:"inherit_env"`
	// EnvFiles name, for a variable of the environment, the file its value
	// is read from at every start of the process, rather than kept in the
	// launch: the password of the admin user of TCM (tcmSpec), which no
	// file but its own holds.
	EnvFiles map[string]string `json:"env_files,omitempty"`
	Dir      string            `json:"dir"`
	// Stdin is what the process reads on its standard input: the launcher
	// of an instance that runs its script.
	Stdin               []byte         `json:"stdin,omitempty"`
	StopSignal          syscall.Signal `json:"stop_signal"`
	ForwardStopSignal   bool           `json:"forward_stop_signal"`
	StopTimeout         time.Duration  `json:"stop_timeout"`
	ReloadSignal        syscall.Signal `json:"reload_signal"`
	ForwardReloadSignal bool           `json:"forward_reload_signal"`
	ProcessGroup        bool           `json:"process_group"`
}

// ResolveLaunch resolves inst into the Launch its watchdog runs: the Spec of
// its process (instanceSpec), checked as a start checks it, on the tarantool
// it is resolved to now, and that tarantool as DescribeTarantool names it.
func ResolveLaunch(inst *InstanceCtx) (Launch, error) {
	base := os.Environ()

	spec, err := instanceSpec(inst, specOptions{
		integrity: inst.Integrity, stdout: nil, stderr: nil, warn: nil,
	})
	if err != nil {
		return Launch{}, err
	}

	launch := Launch{
		Version:           launchVersion,
		Instance:          *inst,
		Spec:              newLaunchSpec(&spec, base),
		Tarantool:         "",
		TarantoolWarnings: nil,
		Watchdog:          0,
	}

	if IsTCM(inst) {
		launch.Spec.EnvFiles = tcmEnvFiles(inst)
	}

	if !IsExternal(inst) {
		described, warnings, err := DescribeTarantool(inst)
		if err == nil {
			launch.Tarantool = described
			launch.TarantoolWarnings = warnings
		}
	}

	return launch, nil
}

// newLaunchSpec returns the LaunchSpec of spec, whose environment starts
// with base, the environment of tt, when it inherits it.
func newLaunchSpec(spec *supervisor.Spec, base []string) LaunchSpec {
	env := spec.Env
	inherit := len(env) >= len(base) && slices.Equal(env[:len(base)], base)

	if inherit {
		env = env[len(base):]
	}

	return LaunchSpec{
		Path:                spec.Path,
		Args:                spec.Args,
		Env:                 slices.Clone(env),
		InheritEnv:          inherit,
		EnvFiles:            nil,
		Dir:                 spec.Dir,
		Stdin:               spec.Stdin,
		StopSignal:          spec.StopSignal,
		ForwardStopSignal:   spec.ForwardStopSignal,
		StopTimeout:         spec.StopTimeout,
		ReloadSignal:        spec.ReloadSignal,
		ForwardReloadSignal: spec.ForwardReloadSignal,
		ProcessGroup:        spec.ProcessGroup,
	}
}

// supervisorSpec returns the supervisor.Spec of the launch, whose output
// goes to stdout and stderr. The variables of EnvFiles are read from their
// files now, on top of the rest of the environment; a file that cannot be
// read is an error.
func (spec *LaunchSpec) supervisorSpec(stdout, stderr io.Writer) (supervisor.Spec, error) {
	env := slices.Clone(spec.Env)
	if spec.InheritEnv {
		env = append(os.Environ(), env...)
	}

	for _, name := range slices.Sorted(maps.Keys(spec.EnvFiles)) {
		value, err := readSecretFile(spec.EnvFiles[name])
		if err != nil {
			return supervisor.Spec{}, fmt.Errorf("the value of %s: %w", name, err)
		}

		env = append(env, name+"="+value)
	}

	return supervisor.Spec{
		Path:                spec.Path,
		Args:                slices.Clone(spec.Args),
		Env:                 env,
		Dir:                 spec.Dir,
		Stdin:               spec.Stdin,
		Stdout:              stdout,
		Stderr:              stderr,
		StopSignal:          spec.StopSignal,
		ForwardStopSignal:   spec.ForwardStopSignal,
		StopTimeout:         spec.StopTimeout,
		ReloadSignal:        spec.ReloadSignal,
		ForwardReloadSignal: spec.ForwardReloadSignal,
		ProcessGroup:        spec.ProcessGroup,
	}, nil
}

// writeLaunchFile writes launch into a file of its own in the run directory
// of its instance, for the watchdog tt instance start starts, and returns
// its path. The file is complete before the watchdog starts.
func writeLaunchFile(launch *Launch) (string, error) {
	//nolint:musttag // The context of the instance is encoded by its field names.
	data, err := json.Marshal(launch)
	if err != nil {
		return "", fmt.Errorf("encoding the launch of the instance: %w", err)
	}

	runDir := launch.Instance.RunDir

	err = os.MkdirAll(runDir, defaultDirPerms)
	if err != nil {
		return "", fmt.Errorf("creating the run directory: %w", err)
	}

	return writeNewFile(runDir, ".start.*.json", data)
}

// writeNewFile writes data into a new file in dir, named after pattern as
// os.CreateTemp names it, with launchFileMode, and returns its path. A file
// that cannot be written whole is removed.
func writeNewFile(dir, pattern string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("writing the launch of the instance: %w", err)
	}

	_, writeErr := file.Write(data)

	err = errors.Join(writeErr, file.Close())
	if err != nil {
		_ = os.Remove(file.Name())

		return "", fmt.Errorf("writing the launch of the instance: %w", err)
	}

	return file.Name(), nil
}

// LoadLaunch reads the launch file at path that tt instance start wrote for
// the watchdog, and removes it. With integrity checks on, the checks of the
// application of the instance are found anew (AppIntegrity): they are not
// in the file.
func LoadLaunch(cmdCtx *cmdcontext.CmdCtx, path string) (*Launch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errLaunch, err)
	}

	_ = os.Remove(path)

	launch, err := decodeLaunch(data)
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", errLaunch, path, err)
	}

	integrityFor := AppIntegrity(cmdCtx)
	if integrityFor != nil {
		inst := &launch.Instance

		integ, err := integrityFor(inst.Scope, inst.AppDir)
		if err != nil {
			return nil, fmt.Errorf("instance %q of %q: %w", inst.InstName, inst.AppName, err)
		}

		inst.Integrity = &integ
	}

	return launch, nil
}

// decodeLaunch decodes a launch of launchVersion.
func decodeLaunch(data []byte) (*Launch, error) {
	var launch Launch

	//nolint:musttag // The context of the instance is encoded by its field names.
	err := json.Unmarshal(data, &launch)
	if err != nil {
		return nil, fmt.Errorf("decoding: %w", err)
	}

	if launch.Version != launchVersion {
		return nil, fmt.Errorf("%w: version %d, not %d", errLaunch, launch.Version,
			launchVersion)
	}

	return &launch, nil
}

// publish writes the launch the watchdog pid runs into launchFileName of the
// run directory, whole: a reader finds the old file or the new one. The
// watchdog holds the pid file of the instance, so no other watchdog writes
// the file meanwhile.
func (launch *Launch) publish(pid int) error {
	published := *launch

	published.Watchdog = pid
	published.Instance.Integrity = nil

	//nolint:musttag // The context of the instance is encoded by its field names.
	data, err := json.Marshal(&published)
	if err != nil {
		return fmt.Errorf("encoding the launch: %w", err)
	}

	written, err := writeNewFile(launch.Instance.RunDir, "."+launchFileName+".*", data)
	if err != nil {
		return err
	}

	err = os.Rename(written, filepath.Join(launch.Instance.RunDir, launchFileName))
	if err != nil {
		_ = os.Remove(written)

		return fmt.Errorf("writing the launch: %w", err)
	}

	return nil
}

// unpublish removes launchFileName of the run directory, before the
// watchdog gives up the pid file of the instance.
func (launch *Launch) unpublish() {
	_ = os.Remove(filepath.Join(launch.Instance.RunDir, launchFileName))
}

// RunningLaunch returns the launch the watchdog of inst runs, read from its
// run directory, or nil when no watchdog runs it, or the one that does has
// written none: a file whose watchdog does not hold the pid file of the
// instance is a leftover of one that is gone.
func RunningLaunch(inst *InstanceCtx) *Launch {
	pid, owned, err := pidfile.Owner(inst.PIDFile)
	if err != nil || !owned || pid == 0 {
		return nil
	}

	data, err := os.ReadFile(filepath.Join(inst.RunDir, launchFileName))
	if err != nil {
		return nil
	}

	launch, err := decodeLaunch(data)
	if err != nil || launch.Watchdog != pid {
		return nil
	}

	return launch
}
