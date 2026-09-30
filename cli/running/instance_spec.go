package running

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
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
}

func integrityOf(cmdCtx *cmdcontext.CmdCtx) *integrity.IntegrityCtx {
	if cmdCtx.Cli.IntegrityCheck == "" {
		return nil
	}

	return &cmdCtx.Integrity
}

// instanceSpec is the Spec of the tarantool process of an instance: an
// instance with a cluster config runs from it, any other one runs its script.
func instanceSpec(cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx, opts specOptions) (
	supervisor.Spec, error,
) {
	if inst.ClusterConfigPath != "" {
		return clusterSpec(cmdCtx.Cli.TarantoolCli.Executable, inst, opts)
	}

	return scriptSpec(cmdCtx.Cli.TarantoolCli.Executable, inst, opts)
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
	}
}

// scriptSpec is the Spec of an instance that runs its script: tarantool reads
// the launcher from its standard input, and the launcher runs the script.
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

func clusterSpec(tarantoolPath string, inst *InstanceCtx, opts specOptions) (
	supervisor.Spec, error,
) {
	_, err := exec.LookPath(tarantoolPath)
	if err != nil {
		return supervisor.Spec{}, fmt.Errorf("looking for the tarantool executable: %w", err)
	}

	args := []string{"-n", inst.InstName, "-c", inst.ClusterConfigPath}
	if opts.integrity != nil {
		args = append(args, "--integrity-check",
			filepath.Join(inst.AppDir, integrity.HashesFileName))
	}

	env := os.Environ()

	for _, dir := range [...][2]string{
		{"TT_VINYL_DIR_DEFAULT", inst.VinylDir},
		{"TT_WAL_DIR_DEFAULT", inst.WalDir},
		{"TT_SNAPSHOT_DIR_DEFAULT", inst.MemtxDir},
	} {
		env = appendEnvIfNotEmpty(env, dir[0], dir[1])
	}

	if inst.RunDir != "" {
		env = append(env, "TT_PROCESS_PID_FILE_DEFAULT="+
			filepath.Join(inst.RunDir, "tarantool.pid"))
	}

	if !util.IsDir(inst.AppDir) {
		return supervisor.Spec{}, fmt.Errorf("%w%q is not a directory",
			errApplicationIsNotADirectory, inst.AppDir)
	}

	consoleSocket, err := shortenSocketPath(inst.ConsoleSocket, inst.AppDir)
	if err != nil {
		return supervisor.Spec{}, err
	}

	env = append(env,
		"PWD="+inst.AppDir,
		"TT_CONSOLE_SOCKET_DEFAULT="+consoleSocket,
		"TT_IPROTO_LISTEN_DEFAULT="+"[{\"uri\":\""+inst.BinaryPort+"\"}]")

	return tarantoolSpec(tarantoolPath, args, env, inst.AppDir, opts), nil
}

func appendEnvIfNotEmpty(env []string, envVarName, value string) []string {
	if value != "" {
		env = append(env, fmt.Sprintf("%s=%s", envVarName, value))
	}

	return env
}

// verifySocketLength refuses a path that does not fit a unix socket address:
// maxSocketPathLinux and maxSocketPathMac are the sizes of sun_path, which
// also holds the terminating NUL.
func verifySocketLength(socketPath string) error {
	maxSocketPath := maxSocketPathLinux
	if runtime.GOOS == "darwin" {
		maxSocketPath = maxSocketPathMac
	}

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
