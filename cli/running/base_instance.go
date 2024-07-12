package running

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/lib/integrity"
)

// baseInstance represents a tarantool instance.
type baseInstance struct {
	// processController is a child process controller.
	*processController
	// logger represents an active logging object.
	logger ttlog.Logger
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
	// binaryPort is a Unix socket to be used as "binary port".
	binaryPort string
	// logDir is log files location.
	logDir string
	// IntegrityCtx contains information necessary to perform integrity checks.
	integrityCtx integrity.IntegrityCtx
	// integrityChecks tells whether integrity checks are turned on.
	integrityChecks bool
	// stdOut is a standard output writer.
	stdOut io.Writer
	// stdErr is a standard error writer.
	stdErr io.Writer
}

func newBaseInstance(tarantoolPath string, instanceCtx InstanceCtx,
	opts ...InstanceOption) baseInstance {
	baseInst := baseInstance{
		tarantoolPath: tarantoolPath,
		appPath:       instanceCtx.InstanceScript,
		appName:       instanceCtx.AppName,
		appDir:        instanceCtx.AppDir,
		instName:      instanceCtx.InstName,
		consoleSocket: instanceCtx.ConsoleSocket,
		walDir:        instanceCtx.WalDir,
		vinylDir:      instanceCtx.VinylDir,
		memtxDir:      instanceCtx.MemtxDir,
		logDir:        instanceCtx.LogDir,
		binaryPort:    instanceCtx.BinaryPort,
		stdOut:        os.Stdout,
		stdErr:        os.Stderr,
	}
	for _, opt := range opts {
		opt(&baseInst)
	}
	return baseInst
}

// InstanceOption is a functional option to configure tarantool instance.
type InstanceOption func(inst *baseInstance) error

// IntegrityOpt sets integrity context.
func IntegrityOpt(integrityCtx integrity.IntegrityCtx) InstanceOption {
	return func(inst *baseInstance) error {
		inst.integrityChecks = true
		inst.integrityCtx = integrityCtx
		return nil
	}
}

// StdOutOpt sets stdout writer for the child process.
func StdOutOpt(writer io.Writer) InstanceOption {
	return func(inst *baseInstance) error {
		inst.stdOut = writer
		return nil
	}
}

// StdErrOpt sets stderr writer for the child process.
func StdErrOpt(writer io.Writer) InstanceOption {
	return func(inst *baseInstance) error {
		inst.stdErr = writer
		return nil
	}
}

// StdLoggerOpt sets logger for the instance and standard out FDs to logger writer.
func StdLoggerOpt(logger ttlog.Logger) InstanceOption {
	return func(inst *baseInstance) error {
		inst.logger = logger
		inst.stdOut = logger.Writer()
		inst.stdErr = logger.Writer()
		return nil
	}
}

// Wait waits for the child process to complete.
func (inst *baseInstance) Wait() error {
	if inst.processController == nil {
		return fmt.Errorf("instance is not started")
	}
	return inst.processController.Wait()
}

// SendSignal sends a signal to tarantool instance.
func (inst *baseInstance) SendSignal(sig os.Signal) error {
	if inst.processController == nil {
		return fmt.Errorf("instance is not started")
	}
	return inst.processController.SendSignal(sig)
}

// IsAlive verifies that the instance is alive by sending a "0" signal.
func (inst *baseInstance) IsAlive() bool {
	if inst.processController == nil {
		return false
	}
	return inst.processController.IsAlive()
}

// StopWithSignal terminates the process with a specific signal.
func (inst *baseInstance) StopWithSignal(waitTimeout time.Duration, usedSignal os.Signal) error {
	if inst.processController == nil {
		return nil
	}
	return inst.processController.StopWithSignal(waitTimeout, usedSignal)
}

// Run runs tarantool instance.
func (inst *baseInstance) Run(opts RunOpts) error {
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
