package running

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/integrity"
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/cli/util"
)

// clusterInstance describes tarantool 3 instance running using cluster config.
type clusterInstance struct {
	// processController is a child process controller.
	processController *processController
	// logger represents an active logging object.
	logger *ttlog.Logger
	// tarantoolPath describes the path to the tarantool binary
	// that will be used to launch the Instance.
	tarantoolPath string
	// appPath describes the path to the "init" file of an application.
	appPath string
	// appName describes the application name (the name of the directory
	// where the application files are present).
	appName string
	// instName describes the instance name.
	instName string
	// walDir is a directory where write-ahead log (.xlog) files are stored.
	walDir string
	// memtxDir is a directory where memtx stores snapshot (.snap) files.
	memtxDir string
	// vinylDir is a directory where vinyl files or subdirectories will be stored.
	vinylDir string
	// consoleSocket is a Unix domain socket to be used as "admin port".
	consoleSocket string
	// appDir is an application directory.
	appDir string
	// runDir is a directory that stores various instance runtime artifacts like
	// console socket, PID file, etc.
	runDir string
	// clusterConfigPath is a path of the cluster config.
	clusterConfigPath string
	// logDir is log files location.
	logDir string
	// integrityChecks tells whether integrity checks are turned on.
	integrityChecks bool
}

// newClusterInstance creates a clusterInstance.
func newClusterInstance(tarantoolCli cmdcontext.TarantoolCli, instanceCtx InstanceCtx,
	logger *ttlog.Logger, integrityChecks bool) (*clusterInstance, error) {
	// Check if tarantool binary exists.
	if _, err := exec.LookPath(tarantoolCli.Executable); err != nil {
		return nil, err
	}

	tntVersion, err := tarantoolCli.GetVersion()
	if err != nil {
		return nil, err
	}
	if tntVersion.Major < 3 {
		return nil, fmt.Errorf("cluster config is supported starting from Tarantool 3.0")
	}

	return &clusterInstance{
		tarantoolPath:     tarantoolCli.Executable,
		appPath:           instanceCtx.InstanceScript,
		appName:           instanceCtx.AppName,
		instName:          instanceCtx.InstName,
		consoleSocket:     instanceCtx.ConsoleSocket,
		logger:            logger,
		walDir:            instanceCtx.WalDir,
		vinylDir:          instanceCtx.VinylDir,
		memtxDir:          instanceCtx.MemtxDir,
		appDir:            instanceCtx.AppDir,
		runDir:            instanceCtx.RunDir,
		clusterConfigPath: instanceCtx.ClusterConfigPath,
		logDir:            instanceCtx.LogDir,
		integrityChecks:   integrityChecks,
	}, nil
}

// appendEnvIfNotEmpty appends environment variable setting if value is not empty.
func appendEnvIfNotEmpty(env []string, envVarName string, value string) []string {
	if value != "" {
		env = append(env, fmt.Sprintf("%s=%s", envVarName, value))
	}
	return env
}

// Start starts tarantool instance with cluster config.
func (inst *clusterInstance) Start() error {
	cmdArgs := []string{"-n", inst.instName, "-c", inst.clusterConfigPath}
	if inst.integrityChecks {
		cmdArgs = append(cmdArgs, "--integrity-check",
			filepath.Join(inst.appDir, integrity.HashesName))
	}

	cmd := exec.Command(inst.tarantoolPath, cmdArgs...)
	cmd.Stdout = inst.logger.Writer()
	cmd.Stderr = inst.logger.Writer()

	cmd.Env = os.Environ()
	cmd.Env = appendEnvIfNotEmpty(cmd.Env, "TT_VINYL_DIR_DEFAULT", inst.vinylDir)
	cmd.Env = appendEnvIfNotEmpty(cmd.Env, "TT_WAL_DIR_DEFAULT", inst.walDir)
	cmd.Env = appendEnvIfNotEmpty(cmd.Env, "TT_SNAPSHOT_DIR_DEFAULT", inst.memtxDir)
	if inst.runDir != "" {
		cmd.Env = append(cmd.Env, "TT_PROCESS_PID_FILE_DEFAULT="+
			filepath.Join(inst.runDir, "tarantool.pid"))
	}
	if util.IsDir(inst.appDir) {
		cmd.Env = append(cmd.Env, "PWD="+inst.appDir)
		cmd.Dir = inst.appDir

		consoleSocket, err := shortenSocketPath(inst.consoleSocket, inst.appDir)
		if err != nil {
			return err
		}
		cmd.Env = append(cmd.Env, "TT_CONSOLE_SOCKET_DEFAULT="+consoleSocket)
	} else {
		return fmt.Errorf("application %q is not a directory", inst.appDir)
	}

	var err error
	if inst.processController, err = newProcessController(cmd); err != nil {
		return err
	}

	return nil
}

// Run runs tarantool instance.
func (inst *clusterInstance) Run(flags RunFlags) error {
	newInstanceEnv := os.Environ()
	args := []string{inst.tarantoolPath}
	args = append(args, convertFlagsToTarantoolOpts(flags)...)
	args = append(args, flags.RunArgs...)

	f, err := integrity.FileRepository.Read(inst.tarantoolPath)
	if err != nil {
		return err
	}
	f.Close()

	log.Debugf("Running Tarantool with args: %s", strings.Join(args[1:], " "))
	execErr := syscall.Exec(inst.tarantoolPath, args, newInstanceEnv)
	if execErr != nil {
		return execErr
	}
	return nil
}

// Wait waits for the process to complete.
func (inst *clusterInstance) Wait() error {
	if inst.processController == nil {
		return fmt.Errorf("instance is not started")
	}
	return inst.processController.Wait()
}

// SendSignal sends a signal to the process.
func (inst *clusterInstance) SendSignal(sig os.Signal) error {
	if inst.processController == nil {
		return fmt.Errorf("instance is not started")
	}
	return inst.processController.SendSignal(sig)
}

// IsAlive verifies that the instance is alive.
func (inst *clusterInstance) IsAlive() bool {
	if inst.processController == nil {
		return false
	}
	return inst.processController.IsAlive()
}

// Stop terminates the process.
//
// timeout - the time that was provided to the process
// to terminate correctly before killing it.
func (inst *clusterInstance) Stop(waitTimeout time.Duration) error {
	if inst.processController == nil {
		return nil
	}
	return inst.processController.Stop(waitTimeout)
}
