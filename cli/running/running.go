package running

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/ttlog"
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
func cleanup(cmdCtx *cmdcontext.CmdCtx) {
	if _, err := os.Stat(cmdCtx.Running.PIDFile); err == nil {
		os.Remove(cmdCtx.Running.PIDFile)
	}

	if _, err := os.Stat(cmdCtx.Running.ConsoleSocket); err == nil {
		os.Remove(cmdCtx.Running.ConsoleSocket)
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
func createLogger(cmdCtx *cmdcontext.CmdCtx) *ttlog.Logger {
	opts := ttlog.LoggerOpts{
		Filename:   cmdCtx.Running.Log,
		MaxSize:    cmdCtx.Running.LogMaxSize,
		MaxBackups: cmdCtx.Running.LogMaxBackups,
		MaxAge:     cmdCtx.Running.LogMaxAge,
	}

	return ttlog.NewLogger(&opts)
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

// createDataDir checks if DataDir folder exists, if not creates it.
func createDataDir(dataDirPath string) error {
	_, err := os.Stat(dataDirPath)
	if err == nil {
		return err
	} else if !os.IsNotExist(err) {
		return fmt.Errorf(`Something went wrong while trying to create the DataDir folder.
			 Error: "%v".`, err)
	}
	// Create a new DataDirfolder.
	// 0770:
	//    user:   read/write/execute
	//    group:  read/write/execute
	//    others: nil
	err = os.Mkdir(dataDirPath, 0770)
	if err != nil {
		return fmt.Errorf(`Something went wrong while trying to create the DataDir folder.
			 Error: "%v".`, err)
	}
	return err
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
func FillCtx(cliOpts *modules.CliOpts, cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) != 1 {
		if len(args) > 1 {
			return fmt.Errorf("Currently, you can specify only one instance at a time.")
		} else {
			return fmt.Errorf("Please specify the name of the application.")
		}
	}

	appName := args[0]
	appPath, err := findAppFile(appName, cliOpts)
	if err != nil {
		return fmt.Errorf("Can't find an application init file: %s", err)
	}

	cmdCtx.Running.AppPath = appPath

	runDir := cliOpts.App.RunDir
	if runDir == "" {
		if runDir, err = os.Getwd(); err != nil {
			return fmt.Errorf(`Can't get the "RunDir: %s"`, err)
		}
	}
	cmdCtx.Running.RunDir = runDir
	cmdCtx.Running.ConsoleSocket = filepath.Join(runDir, appName+".control")
	cmdCtx.Running.PIDFile = filepath.Join(runDir, appName+".pid")

	cmdCtx.Running.LogDir = cliOpts.App.LogDir
	cmdCtx.Running.Log, err = util.JoinAbspath(cmdCtx.Running.LogDir, appName+".log")
	if err != nil {
		return fmt.Errorf("Can't get the log file name: %s", err)
	}
	cmdCtx.Running.LogMaxSize = cliOpts.App.LogMaxSize
	cmdCtx.Running.LogMaxAge = cliOpts.App.LogMaxAge
	cmdCtx.Running.LogMaxBackups = cliOpts.App.LogMaxBackups
	if cliOpts.App.DataDir == "" {
		curDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cmdCtx.Running.DataDir = filepath.Join(curDir, appName+"_data_dir")
	} else {
		cmdCtx.Running.DataDir = cliOpts.App.DataDir
	}
	err = createDataDir(cmdCtx.Running.DataDir)
	if err != nil {
		return err
	}
	return nil
}

// Start an Instance.
func Start(cmdCtx *cmdcontext.CmdCtx) error {
	if err := createPIDFile(cmdCtx.Running.PIDFile); err != nil {
		return err
	}

	defer cleanup(cmdCtx)

	logger := createLogger(cmdCtx)

	inst, err := NewInstance(cmdCtx.Cli.TarantoolExecutable, cmdCtx.Running.AppPath,
		cmdCtx.Running.ConsoleSocket, os.Environ(), logger, cmdCtx.Running.DataDir)
	if err != nil {
		return err
	}

	wd := NewWatchdog(inst, cmdCtx.Running.Restartable, 5*time.Second, logger)
	wd.Start()

	return nil
}

// Stop the Instance.
func Stop(cmdCtx *cmdcontext.CmdCtx) error {
	pid, err := getPIDFromFile(cmdCtx.Running.PIDFile)
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

	// tarantool 1.10 does not have a trigger on terminate a process.
	// So the socket will be closed automatically on termination and
	// we need to delete the file.
	if _, err := os.Stat(cmdCtx.Running.ConsoleSocket); err == nil {
		os.Remove(cmdCtx.Running.ConsoleSocket)
	}

	log.Printf("The Instance (PID = %v) has been terminated.\n", pid)

	return nil
}

// Status returns the status of the Instance.
func Status(cmdCtx *cmdcontext.CmdCtx) string {
	pid, err := getPIDFromFile(cmdCtx.Running.PIDFile)
	if err != nil {
		return fmt.Sprintf(`NOT RUNNING. Can't get the PID of process: "%v".`, err)
	}

	alive, err := isProcessAlive(pid)
	if !alive {
		return fmt.Sprintf("ERROR. The process is dead.")
	}

	return fmt.Sprintf("RUNNING. PID: %v.", pid)
}

// Logrotate rotates logs of a started tarantool instance.
func Logrotate(cmdCtx *cmdcontext.CmdCtx) (string, error) {
	pid, err := getPIDFromFile(cmdCtx.Running.PIDFile)
	if err != nil {
		return "", fmt.Errorf(`NOT RUNNING. Can't get the PID of process: "%v".`, err)
	}

	alive, err := isProcessAlive(pid)
	if !alive {
		return "", fmt.Errorf("ERROR. The process is dead.")
	}

	if err := syscall.Kill(pid, syscall.Signal(syscall.SIGHUP)); err != nil {
		return "", fmt.Errorf(`Can't rotate logs: "%v".`, err)
	}

	// Rotates logs [instance name pid]
	return fmt.Sprintf("Logs has been rotated. PID: %v.", pid), nil
}

// Check returns the result of checking the syntax of the application file.
func Check(cmdCtx *cmdcontext.CmdCtx) error {
	var errbuff bytes.Buffer
	os.Setenv("TT_CLI_INSTANCE", cmdCtx.Running.AppPath)

	cmd := exec.Command(cmdCtx.Cli.TarantoolExecutable, "-e", checkSyntax)
	cmd.Stderr = &errbuff
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(errbuff.String())
	}

	return nil
}
