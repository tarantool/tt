package running

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/integrity"
)

// clusterInstance describes tarantool 3 instance running using cluster config.
type clusterInstance struct {
	baseInstance
	// clusterConfigPath is a path of the cluster config.
	clusterConfigPath string
	runDir            string
}

// newClusterInstance creates a clusterInstance.
func newClusterInstance(tarantoolCli cmdcontext.TarantoolCli, instanceCtx InstanceCtx,
	opts ...InstanceOption,
) (*clusterInstance, error) {
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
		baseInstance:      newBaseInstance(tarantoolCli.Executable, instanceCtx, opts...),
		runDir:            instanceCtx.RunDir,
		clusterConfigPath: instanceCtx.ClusterConfigPath,
	}, nil
}

// appendEnvIfNotEmpty appends environment variable setting if value is not empty.
func appendEnvIfNotEmpty(env []string, envVarName, value string) []string {
	if value != "" {
		env = append(env, fmt.Sprintf("%s=%s", envVarName, value))
	}
	return env
}

// Start starts tarantool instance with cluster config.
func (inst *clusterInstance) Start(ctx context.Context) error {
	cmdArgs := []string{"-n", inst.instName, "-c", inst.clusterConfigPath}
	if inst.integrityChecks {
		cmdArgs = append(cmdArgs, "--integrity-check",
			filepath.Join(inst.appDir, integrity.HashesFileName))
	}

	cmd := exec.CommandContext(ctx, inst.tarantoolPath, cmdArgs...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 30 * time.Second
	cmd.Stdout = inst.stdOut
	cmd.Stderr = inst.stdErr

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
		cmd.Env = append(cmd.Env, "TT_IPROTO_LISTEN_DEFAULT="+"[{\"uri\":\""+inst.binaryPort+"\"}]")
	} else {
		return fmt.Errorf("application %q is not a directory", inst.appDir)
	}

	var err error
	if inst.processController, err = newProcessController(cmd); err != nil {
		return err
	}

	return nil
}
