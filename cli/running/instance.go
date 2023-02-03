package running

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/ttlog"
)

const (
	maxSocketPathLinux = 108
	maxSocketPathMac   = 106
)

// Instance describes a running process.
type Instance struct {
	// Cmd represents an external command being prepared and run.
	Cmd *exec.Cmd
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
	memtxDir string `mapstructure:"memtx_dir" yaml:"memtx_dir"`
	// vinylDir is a directory where vinyl files or subdirectories will be stored.
	vinylDir string `mapstructure:"vinyl_dir" yaml:"vinyl_dir"`
	// env describes the environment settled by a client.
	env []string
	// consoleSocket is a Unix domain socket to be used as "admin port".
	consoleSocket string
	// waitMutex is used to prevent several invokes of the "Wait"
	// for the same process.
	// https://github.com/golang/go/issues/28461
	waitMutex sync.Mutex
	// done represent whether the instance was stopped.
	done bool
}

//go:embed lua/launcher.lua
var instanceLauncher []byte

// NewInstance creates an Instance.
func NewInstance(tarantoolPath string, instanceCtx *InstanceCtx, env []string,
	logger *ttlog.Logger) (*Instance, error) {
	// Check if tarantool binary exists.
	if _, err := exec.LookPath(tarantoolPath); err != nil {
		return nil, err
	}

	// Check if Application exists.
	if _, err := os.Stat(instanceCtx.AppPath); err != nil {
		return nil, err
	}

	return &Instance{
		tarantoolPath: tarantoolPath,
		appPath:       instanceCtx.AppPath,
		appName:       instanceCtx.AppName,
		instName:      instanceCtx.InstName,
		consoleSocket: instanceCtx.ConsoleSocket,
		env:           env,
		logger:        logger,
		walDir:        instanceCtx.WalDir,
		vinylDir:      instanceCtx.VinylDir,
		memtxDir:      instanceCtx.MemtxDir,
	}, nil
}

// SendSignal sends a signal to the Instance.
func (inst *Instance) SendSignal(sig os.Signal) error {
	if inst.Cmd == nil {
		return fmt.Errorf("the instance hasn't started yet")
	}
	return inst.Cmd.Process.Signal(sig)
}

// IsAlive verifies that the Instance is alive by sending a "0" signal.
func (inst *Instance) IsAlive() bool {
	return inst.SendSignal(syscall.Signal(0)) == nil
}

// Wait calls "Wait" for the process.
func (inst *Instance) Wait() error {
	if inst.done {
		return nil
	}
	// waitMutex is used to prevent several invokes of the "Wait"
	// for the same process.
	// https://github.com/golang/go/issues/28461
	inst.waitMutex.Lock()
	defer inst.waitMutex.Unlock()
	err := inst.Cmd.Wait()
	if err == nil {
		inst.done = true
	}
	return err
}

// Start starts the Instance with the specified parameters.
func (inst *Instance) Start() error {
	inst.Cmd = exec.Command(inst.tarantoolPath, "-")
	inst.Cmd.Stdout = inst.logger.Writer()
	inst.Cmd.Stderr = inst.logger.Writer()
	StdinPipe, err := inst.Cmd.StdinPipe()
	if err != nil {
		return err
	}
	inst.Cmd.Env = append(os.Environ(), "TT_CLI_INSTANCE="+inst.appPath)

	// It became common that console socket path is longer than 108/106 (on linux/macOs).
	// To reduce length of path we use relative path
	// with chdir into a directory of console socket.
	// e.g foo/bar/123.sock -> ./123.sock

	maxSocketPath := maxSocketPathLinux
	if runtime.GOOS == "darwin" {
		maxSocketPath = maxSocketPathMac
	}

	if inst.consoleSocket != "" {
		if len("./"+filepath.Base(inst.consoleSocket))+1 > maxSocketPath {
			return fmt.Errorf("socket name is longer than %d symbols: %s",
				maxSocketPath-3, filepath.Base(inst.consoleSocket))
		}
		inst.Cmd.Env = append(inst.Cmd.Env,
			"TT_CLI_CONSOLE_SOCKET="+"unix/:./"+filepath.Base(inst.consoleSocket))
		inst.Cmd.Env = append(inst.Cmd.Env,
			"TT_CLI_CONSOLE_SOCKET_DIR="+filepath.Dir(inst.consoleSocket))
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_CLI_WORK_DIR="+workDir)
	// Imitate the "tarantoolctl".
	inst.Cmd.Env = append(inst.Cmd.Env, "TARANTOOLCTL=true")
	// Set the sign that the program is running under "tt".
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_CLI=true")
	// Set the wal, memtx and vinyls dirs.
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_VINYL_DIR="+inst.vinylDir)
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_WAL_DIR="+inst.walDir)
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_MEMTX_DIR="+inst.memtxDir)

	// Setup variables for the cartridge application compatibility.
	if inst.instName != "stateboard" {
		inst.Cmd.Env = append(inst.Cmd.Env, "TARANTOOL_APP_NAME="+inst.appName)
		inst.Cmd.Env = append(inst.Cmd.Env, "TARANTOOL_INSTANCE_NAME="+inst.instName)
	} else {
		inst.Cmd.Env = append(inst.Cmd.Env, "TARANTOOL_APP_NAME="+inst.appName+"-"+inst.instName)
	}
	if inst.appName != inst.instName {
		inst.Cmd.Env = append(inst.Cmd.Env,
			"TARANTOOL_CFG="+filepath.Dir(inst.appPath)+"/instances.yml")
	}
	inst.Cmd.Env = append(inst.Cmd.Env, "TARANTOOL_WORKDIR="+inst.walDir)

	// Start an Instance.
	if err := inst.Cmd.Start(); err != nil {
		return err
	}
	StdinPipe.Write([]byte(instanceLauncher))
	StdinPipe.Close()
	inst.done = false

	return nil
}

// convertFlagsToTarantoolOpts turns flags into tarantool command.
func convertFlagsToTarantoolOpts(flags RunFlags) []string {
	var command []string
	if flags.RunEval != "" {
		command = append(command, "-e")
		command = append(command, flags.RunEval)
	}
	if flags.RunInteractive {
		command = append(command, "-i")
	}
	if flags.RunLib != "" {
		command = append(command, "-l")
		command = append(command, flags.RunLib)
	}
	if flags.RunVersion {
		command = append(command, "-v")
	}
	return command
}

// Run runs tarantool instance.
func (inst *Instance) Run(flags RunFlags) error {
	newInstanceEnv := os.Environ()
	newInstanceEnv = append(newInstanceEnv,
		"TT_CLI_INSTANCE="+inst.appPath,
		"TT_CLI=true",
	)
	args := []string{inst.tarantoolPath}
	args = append(args, convertFlagsToTarantoolOpts(flags)...)
	if inst.appPath != "" {
		log.Debugf("Script to run: %s", inst.appPath)

		// Save current stdin file descriptor. It will be used in launcher lua
		// script to restore original stdin.
		stdinFd, _ := syscall.Dup(int(os.Stdin.Fd()))
		newInstanceEnv = append(newInstanceEnv, fmt.Sprintf("TT_CLI_RUN_STDIN_FD=%d", stdinFd))

		// Replace current stdin with pipe descriptor, and write launcher code to this pipe.
		// Tarantool will read from pipe after exec.
		stdinReader, stdinWriter, _ := os.Pipe()
		syscall.Dup2(int(stdinReader.Fd()), int(os.Stdin.Fd()))
		stdinWriter.Write([]byte(instanceLauncher))

		// Enable reading from input for Tarantool.
		args = append(args, "-")
	}
	args = append(args, flags.RunArgs...)
	log.Debugf("Running Tarantool with args: %s", strings.Join(args[1:], " "))
	execErr := syscall.Exec(inst.tarantoolPath, args, newInstanceEnv)
	if execErr != nil {
		return execErr
	}
	return nil
}

// Stop terminates the Instance.
//
// timeout - the time that was provided to the process
// to terminate correctly before the "SIGKILL" signal is used.
func (inst *Instance) Stop(timeout time.Duration) error {
	if !inst.IsAlive() {
		return nil
	}

	// Сreate a channel to receive an indication of the termination
	// of the Instance.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- inst.Wait()
	}()

	// Trying to terminate the process by using a "SIGINT" signal.
	// In case of failure a "SIGKILL" signal will be used.
	if err := inst.SendSignal(os.Interrupt); err != nil {
		return fmt.Errorf("failed to send SIGINT to instance: %s", err)
	}

	// Terminate the Instance at any cost.
	select {
	case <-time.After(timeout):
		// Send "SIGKILL" signal
		if err := inst.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to send SIGKILL to instance: %s", err)
		} else {
			// Wait for the process to terminate.
			_ = <-waitDone
			return nil
		}
	case err := <-waitDone:
		return err
	}
}
