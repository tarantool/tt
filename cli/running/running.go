package running

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/tarantool/tt/cli/context"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// findAppFile searches of an application init file.
func findAppFile(appName string, cliOpts *modules.CliOpts) (string, error) {
	var err error
	appDir := cliOpts.App.InstancesAvailable
	if appDir == "" {
		if appDir, err = os.Getwd(); err != nil {
			return "", err
		}
	}

	var appPath string

	// We considering several scenarios:
	// 1) The application starts by `appName.lua`
	// 2) The application starts by `appName/init.lua`
	appAbsPath, err := util.JoinAbspath(appDir, appName+".lua")
	if err != nil {
		return "", err
	}
	dirAbsPath, err := util.JoinAbspath(appDir, appName)
	if err != nil {
		return "", err
	}

	// Check if one or both file and/or directory exist.
	_, fileStatErr := os.Stat(appAbsPath)
	dirInfo, dirStatErr := os.Stat(dirAbsPath)

	if !os.IsNotExist(fileStatErr) {
		if fileStatErr != nil {
			return "", fileStatErr
		}
		appPath = appAbsPath
	} else if dirStatErr == nil && dirInfo.IsDir() {
		appPath = path.Join(dirAbsPath, "init.lua")
		if _, err = os.Stat(appPath); err != nil {
			return "", err
		}
	} else {
		return "", fileStatErr
	}

	return appPath, nil
}

// cleanup removes runtime artifacts.
func cleanup(ctx *context.Ctx) {
	if _, err := os.Stat(ctx.Running.PIDFile); err == nil {
		os.Remove(ctx.Running.PIDFile)
	}

	if _, err := os.Stat(ctx.Running.ConsoleSocket); err == nil {
		os.Remove(ctx.Running.ConsoleSocket)
	}
}

// getPIDFromFile returns PID from the PIDFile.
func getPIDFromFile(pidFileName string) (int, error) {
	if _, err := os.Stat(pidFileName); err != nil {
		return 0, fmt.Errorf(`Can't "stat" the PID file. Error: "%v".`, err)
	}

	pidFile, err := os.Open(pidFileName)
	if err != nil {
		return 0, fmt.Errorf(`Can't open the PID file. Error: "%v".`, err)
	}

	pidBytes, err := ioutil.ReadAll(pidFile)
	if err != nil {
		return 0, fmt.Errorf(`Can't read the PID file. Error: "%v".`, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return 0,
			fmt.Errorf(`PID file exists with unknown format. Error: "%s"`, err)
	}

	return pid, nil
}

// createLogger prepares a logger for the watchdog and instance.
func createLogger(ctx *context.Ctx) *log.Logger {
	// We use a wrapper for the logger, which is responsible for
	// writing and rotating the logs.
	writer := lumberjack.Logger{
		Filename:   ctx.Running.Log,
		MaxSize:    ctx.Running.LogMaxSize,
		MaxBackups: ctx.Running.LogMaxBackups,
		MaxAge:     ctx.Running.LogMaxAge,
		Compress:   false,
		LocalTime:  true,
	}
	log.SetOutput(&writer)

	return log.Default()
}

// isProcessAlive checks if the process is alive.
func isProcessAlive(pid int) (bool, error) {
	// The signal 0 is used to check if a process is alive.
	// From `man 2 kill`:
	// If  sig  is  0,  then  no  signal is sent, but existence and permission
	// checks are still performed; this can be used to check for the existence
	// of  a  process  ID  or process group ID that the caller is permitted to
	// signal.
	if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
		return false, err
	}

	return true, nil
}

// waitProcessTermination waits while the process will be terminated.
// Returns true if the process was terminated and false if is steel alive.
func waitProcessTermination(pid int, timeout time.Duration,
	checkPeriod time.Duration) bool {
	if res, _ := isProcessAlive(pid); !res {
		return true
	}

	result := false
	breakTimer := time.NewTimer(timeout)
loop:
	for {
		select {
		case <-breakTimer.C:
			if res, _ := isProcessAlive(pid); !res {
				result = true
			}
			break loop
		case <-time.After(checkPeriod):
			if res, _ := isProcessAlive(pid); !res {
				result = true
				break loop
			}
		}
	}

	return result
}

// createPIDFile checks that the instance PID file is absent or
// deprecated and creates a new one. Returns an error on failure.
func createPIDFile(pidFileName string) error {
	if _, err := os.Stat(pidFileName); err == nil {
		// The PID file already exists. We have to check if the process is alive.
		pid, err := getPIDFromFile(pidFileName)
		if err != nil {
			return fmt.Errorf(`PID file exists, but PID can't be read. Error: "%v".`, err)
		}
		if res, _ := isProcessAlive(pid); res {
			return fmt.Errorf("The Instance is already exists.")
		} else {
			os.Remove(pidFileName)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf(`Something went wrong while trying to read the PID file. Error: "%v".`,
			err)
	}

	// Create a new PID file.
	// 0644:
	//    user:   read/write
	//    group:  read
	//    others: read
	pidFile, err := os.OpenFile(pidFileName,
		syscall.O_EXCL|syscall.O_CREAT|syscall.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf(`Can't create a new PID file. Error: "%v".`, err)
	}
	defer pidFile.Close()

	if _, err = pidFile.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		return err
	}

	return nil
}

// FillCtx fills the RunningCtx context.
func FillCtx(cliOpts *modules.CliOpts, ctx *context.Ctx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("Currently, you can specify only one instance at a time.")
	}

	appName := args[0]
	appPath, err := findAppFile(appName, cliOpts)
	if err != nil {
		return fmt.Errorf("Can't find an application init file: %s", err)
	}

	ctx.Running.AppPath = appPath

	runDir := cliOpts.App.RunDir
	if runDir == "" {
		if runDir, err = os.Getwd(); err != nil {
			return fmt.Errorf(`Can't get the "RunDir: %s"`, err)
		}
	}
	ctx.Running.RunDir = runDir
	ctx.Running.ConsoleSocket = filepath.Join(runDir, appName+".control")
	ctx.Running.PIDFile = filepath.Join(runDir, appName+".pid")

	ctx.Running.LogDir = cliOpts.App.LogDir
	ctx.Running.Log, err = util.JoinAbspath(ctx.Running.LogDir, appName+".log")
	if err != nil {
		return fmt.Errorf("Can't get the log file name: %s", err)
	}
	ctx.Running.LogMaxSize = cliOpts.App.LogMaxSize
	ctx.Running.LogMaxAge = cliOpts.App.LogMaxAge
	ctx.Running.LogMaxBackups = cliOpts.App.LogMaxBackups

	return nil
}

// Start an Instance.
func Start(ctx *context.Ctx) error {
	if err := createPIDFile(ctx.Running.PIDFile); err != nil {
		return err
	}

	defer cleanup(ctx)

	logger := createLogger(ctx)

	inst, err := NewInstance(ctx.Cli.TarantoolExecutable,
		ctx.Running.AppPath, ctx.Running.ConsoleSocket, os.Environ(), logger)
	if err != nil {
		return err
	}

	wd := NewWatchdog(inst, ctx.Running.Restartable, 5*time.Second, logger)
	wd.Start()

	return nil
}

// Stop the Instance.
func Stop(ctx *context.Ctx) error {
	pid, err := getPIDFromFile(ctx.Running.PIDFile)
	if err != nil {
		return err
	}

	alive, err := isProcessAlive(pid)
	if !alive {
		return fmt.Errorf(`The instance is already dead. Error: "%v".`, err)
	}

	if err = syscall.Kill(pid, syscall.SIGINT); err != nil {
		return fmt.Errorf(`Can't terminate the instance. Error: "%v".`, err)
	}

	if res := waitProcessTermination(pid, 30*time.Second, 100*time.Millisecond); !res {
		return fmt.Errorf("Can't terminate the instance.")
	}

	log.Printf("The Instance (PID = %v) has been terminated.\n", pid)

	return nil
}

// Status returns the status of the Instance.
func Status(ctx *context.Ctx) string {
	pid, err := getPIDFromFile(ctx.Running.PIDFile)
	if err != nil {
		return fmt.Sprintf(`NOT RUNNING. Can't get the PID of process: "%v".`, err)
	}

	alive, err := isProcessAlive(pid)
	if !alive {
		return fmt.Sprintf("ERROR. The process is dead.")
	}

	return fmt.Sprintf("RUNNING. PID: %v.", pid)
}
