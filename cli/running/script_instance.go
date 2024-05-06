package running

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/integrity"
)

const (
	maxSocketPathLinux = 108
	maxSocketPathMac   = 104
)

// scriptInstance represents a tarantool invoked with an instance script provided.
type scriptInstance struct {
	// processController is a child process controller.
	processController *processController
	// logger represents an active logging object.
	logger *ttlog.Logger
	// tarantoolPath describes the path to the tarantool binary
	// that will be used to launch the Instance.
	tarantoolPath string
	// appPath describes the path to the "init" file of an application.
	appPath string
	// appDir is an application directory.
	appDir string
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
	// consoleSocket is a Unix domain socket to be used as "admin port".
	consoleSocket string
	// binaryPort is a Unix socket to be used as "binary port"
	binaryPort string
	// logDir is log files location.
	logDir string
	// IntegrityCtx contains information necessary to perform integrity checks.
	integrityCtx integrity.IntegrityCtx
	// integrityChecks tells whether integrity checks are turned on.
	integrityChecks bool
}

//go:embed lua/launcher.lua
var instanceLauncher []byte

// newScriptInstance creates an Instance.
func newScriptInstance(tarantoolPath string, instanceCtx InstanceCtx, logger *ttlog.Logger,
	integrityCtx integrity.IntegrityCtx, integrityChecks bool) (*scriptInstance, error) {
	// Check if tarantool binary exists.
	if _, err := exec.LookPath(tarantoolPath); err != nil {
		return nil, err
	}

	// Check if Application exists.
	if _, err := os.Stat(instanceCtx.InstanceScript); err != nil {
		return nil, err
	}

	return &scriptInstance{
		tarantoolPath:   tarantoolPath,
		appPath:         instanceCtx.InstanceScript,
		appName:         instanceCtx.AppName,
		appDir:          instanceCtx.AppDir,
		instName:        instanceCtx.InstName,
		consoleSocket:   instanceCtx.ConsoleSocket,
		logger:          logger,
		walDir:          instanceCtx.WalDir,
		vinylDir:        instanceCtx.VinylDir,
		memtxDir:        instanceCtx.MemtxDir,
		logDir:          instanceCtx.LogDir,
		integrityCtx:    integrityCtx,
		integrityChecks: integrityChecks,
		binaryPort:      instanceCtx.BinaryPort,
	}, nil
}

// verifySocketLength makes sure socket path length is in bounds.
func verifySocketLength(socketPath string) error {
	maxSocketPath := maxSocketPathLinux
	if runtime.GOOS == "darwin" {
		maxSocketPath = maxSocketPathMac
	}

	if socketPath != "" {
		if len(socketPath) >= maxSocketPath {
			return fmt.Errorf("socket path is longer than %d symbols: %q",
				maxSocketPath-1, socketPath)
		}
		return nil
	}
	return nil
}

// shortenSocketPath reduces the length of console socket path.
// It became common that console socket path is longer than 108/106 (on linux/macOs).
func shortenSocketPath(socketPath string, basePath string) (string, error) {
	if err := verifySocketLength(socketPath); err == nil {
		return socketPath, nil
	}

	var err error
	var relativeSocketPath string
	if relativeSocketPath, err = filepath.Rel(basePath, socketPath); err == nil {
		if err = verifySocketLength(relativeSocketPath); err == nil {
			return relativeSocketPath, nil
		}
	}
	return "", err
}

// setTarantoolLog sets tarantool log file path env var.
func (inst *scriptInstance) setTarantoolLog(cmd *exec.Cmd) {
	if inst.logDir != "" {
		// This is a cartridge specific variable required for logging scheme to work with
		// tarantool <2.11 versions. Cartridge performs logging configuration before box.cfg
		// and uses its own variables to pass to log.cfg call.
		cmd.Env = append(cmd.Env, "TARANTOOL_LOG=")
	}
}

// Start starts the Instance with the specified parameters.
func (inst *scriptInstance) Start() error {
	f, err := inst.integrityCtx.Repository.Read(inst.tarantoolPath)
	if err != nil {
		return err
	}
	f.Close()

	cmdArgs := []string{}

	if inst.integrityChecks {
		cmdArgs = append(cmdArgs, "--integrity-check", integrity.HashesFileName)
	}

	cmdArgs = append(cmdArgs, "-")

	cmd := exec.Command(inst.tarantoolPath, cmdArgs...)
	cmd.Stdout = inst.logger.Writer()
	cmd.Stderr = inst.logger.Writer()
	StdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Env = append(os.Environ(), "TT_CLI_INSTANCE="+inst.appPath)
	if inst.appDir == "" {
		inst.appDir = filepath.Dir(inst.appPath)
	}
	if !util.IsDir(inst.appDir) {
		if err := os.MkdirAll(inst.appDir, defaultDirPerms); err != nil {
			return fmt.Errorf("failed to create application directory %q: %w", inst.appDir, err)
		}
	}
	workDir := inst.appDir
	cmd.Env = append(cmd.Env, "PWD="+workDir)
	cmd.Dir = workDir
	_, listenSet := os.LookupEnv("TT_LISTEN")
	if inst.binaryPort != "" && inst.instName != stateBoardInstName && !listenSet {
		cmd.Env = append(cmd.Env, "TT_LISTEN="+inst.binaryPort)
	}
	if inst.consoleSocket != "" {
		consoleSocket, err := shortenSocketPath(inst.consoleSocket, workDir)
		if err != nil {
			return err
		}
		cmd.Env = append(cmd.Env,
			"TT_CLI_CONSOLE_SOCKET="+"unix/:./"+filepath.Base(consoleSocket))
		cmd.Env = append(cmd.Env,
			"TT_CLI_CONSOLE_SOCKET_DIR="+filepath.Dir(consoleSocket))
	}
	cmd.Env = append(cmd.Env,
		"TT_CLI_INSTANCE="+inst.appPath,
		"TT_CLI_WORK_DIR="+workDir,
		"TARANTOOLCTL=true", // Imitate the "tarantoolctl".
		"TT_CLI=true",       // Set the sign that the program is running under "tt".
		"TT_VINYL_DIR="+inst.vinylDir,
		"TT_WAL_DIR="+inst.walDir,
		"TT_MEMTX_DIR="+inst.memtxDir,
		"TARANTOOL_WORKDIR="+inst.walDir)

	// Setup variables for the cartridge application compatibility.
	if inst.instName != stateBoardInstName {
		cmd.Env = append(cmd.Env, "TARANTOOL_APP_NAME="+inst.appName)
		cmd.Env = append(cmd.Env, "TARANTOOL_INSTANCE_NAME="+inst.instName)
	} else {
		cmd.Env = append(cmd.Env, "TARANTOOL_APP_NAME="+inst.appName+"-"+inst.instName)
	}
	if inst.appName != inst.instName {
		cmd.Env = append(cmd.Env,
			"TARANTOOL_CFG="+filepath.Dir(inst.appPath)+"/instances.yml")
	}

	inst.setTarantoolLog(cmd)

	// Start an Instance.
	if inst.processController, err = newProcessController(cmd); err != nil {
		return err
	}
	StdinPipe.Write([]byte(instanceLauncher))
	StdinPipe.Close()

	return nil
}

// Run runs tarantool instance.
func (inst *scriptInstance) Run(opts RunOpts) error {
	f, err := inst.integrityCtx.Repository.Read(inst.tarantoolPath)
	if err != nil {
		return err
	}
	f.Close()
	newInstanceEnv := os.Environ()
	args := []string{inst.tarantoolPath}
	args = append(args, opts.RunArgs...)
	log.Debugf("Running Tarantool with args: %s", strings.Join(args[1:], " "))
	execErr := syscall.Exec(inst.tarantoolPath, args, newInstanceEnv)
	if execErr != nil {
		return execErr
	}
	return nil
}

// Wait waits for the process completion.
func (inst *scriptInstance) Wait() error {
	if inst.processController == nil {
		return fmt.Errorf("instance is not started")
	}
	return inst.processController.Wait()
}

// SendSignal sends a signal to tarantool instance.
func (inst *scriptInstance) SendSignal(sig os.Signal) error {
	if inst.processController == nil {
		return fmt.Errorf("instance is not started")
	}
	return inst.processController.SendSignal(sig)
}

// IsAlive verifies that the instance is alive by sending a "0" signal.
func (inst *scriptInstance) IsAlive() bool {
	if inst.processController == nil {
		return false
	}
	return inst.processController.IsAlive()
}

// Stop terminates the process.
//
// timeout - the time that was provided to the process
// to terminate correctly before the "SIGKILL" signal is used.
func (inst *scriptInstance) Stop(waitTimeout time.Duration) error {
	if inst.processController == nil {
		return nil
	}
	return inst.processController.Stop(waitTimeout)
}

// StopWithSignal terminates the process with specific signal.
func (inst *scriptInstance) StopWithSignal(waitTimeout time.Duration, usedSignal os.Signal) error {
	if inst.processController == nil {
		return nil
	}
	return inst.processController.StopWithSignal(waitTimeout, usedSignal)
}
