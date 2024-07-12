package running

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/integrity"
)

const (
	maxSocketPathLinux = 108
	maxSocketPathMac   = 104
)

// scriptInstance represents a tarantool invoked with an instance script provided.
type scriptInstance struct {
	baseInstance
}

//go:embed lua/launcher.lua
var instanceLauncher []byte

// newScriptInstance creates an Instance.
func newScriptInstance(tarantoolPath string, instanceCtx InstanceCtx, opts ...InstanceOption) (
	*scriptInstance, error) {
	// Check if tarantool binary exists.
	if _, err := exec.LookPath(tarantoolPath); err != nil {
		return nil, err
	}

	// Check if Application exists.
	if _, err := os.Stat(instanceCtx.InstanceScript); err != nil {
		return nil, err
	}

	return &scriptInstance{
		baseInstance: newBaseInstance(tarantoolPath, instanceCtx, opts...),
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
func (inst *scriptInstance) Start(ctx context.Context) error {
	if inst.integrityChecks {
		f, err := inst.integrityCtx.Repository.Read(inst.tarantoolPath)
		if err != nil {
			return err
		}
		f.Close()
	}

	cmdArgs := []string{}

	if inst.integrityChecks {
		cmdArgs = append(cmdArgs, "--integrity-check", integrity.HashesFileName)
	}

	cmdArgs = append(cmdArgs, "-")

	cmd := exec.CommandContext(ctx, inst.tarantoolPath, cmdArgs...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 30 * time.Second
	cmd.Stdout = inst.stdOut
	cmd.Stderr = inst.stdErr
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
