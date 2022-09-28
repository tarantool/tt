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
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/ttlog"
	"gopkg.in/yaml.v2"
)

const defaultDirPerms = 0770

const (
	InstStateStopped = "NOT RUNNING."
	InstStateDead    = "ERROR. The process is dead."
	InstStateRunning = "RUNNING. PID: %v."
)

// RunFlags contains flags for tt run.
type RunFlags struct {
	// RunEval contains "-e" flag content.
	RunEval string
	// RunLib contains "-l" flag content.
	RunLib string
	// RunInteractive contains "-i" flag content.
	RunInteractive bool
	// RunStdin contains "-" flag content.
	RunStdin string
	// RunVersion contains "-v" flag content.
	RunVersion bool
	// RunArgs contains command args.
	RunArgs []string
}

// RunOpts contains information for tt run.
type RunOpts struct {
	CmdCtx   *cmdcontext.CmdCtx
	RunFlags *RunFlags
}

// providerImpl is an implementation of Provider interface.
type providerImpl struct {
	cmdCtx *cmdcontext.CmdCtx
	// instance is a pointer to the specific data of the instance to work with.
	instance *cmdcontext.RunningCtx
}

// updateCtx updates cmdCtx according to the current contents of the cfg file.
func (provider *providerImpl) updateCtx() error {
	cliOpts, err := configure.GetCliOpts(provider.cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	args := []string{provider.instance.AppName}
	if err = FillCtx(cliOpts, provider.cmdCtx, args); err != nil {
		return err
	}

	return nil
}

// createInstance reads config and creates an Instance.
func (provider *providerImpl) CreateInstance(logger *ttlog.Logger) (*Instance, error) {
	if err := provider.updateCtx(); err != nil {
		return nil, err
	}

	inst, err := NewInstance(provider.cmdCtx.Cli.TarantoolExecutable,
		provider.instance.AppPath, provider.instance.AppName, provider.instance.InstName,
		provider.instance.ConsoleSocket, os.Environ(), logger, provider.instance.DataDir)
	if err != nil {
		return nil, err
	}
	return inst, nil
}

// isLoggerChanged checks if any of the logging parameters has been changed.
func isLoggerChanged(logger *ttlog.Logger, runningCtx *cmdcontext.RunningCtx) (bool, error) {
	if runningCtx == nil {
		return true, fmt.Errorf("RunningCtx, which is used to check if the logger parameters" +
			" are updated, is nil.")
	}
	if logger == nil || runningCtx == nil {
		return true, nil
	}
	loggerOpts := logger.GetOpts()

	// Check if some of the parameters have been changed.
	if loggerOpts.Filename != runningCtx.Log {
		return true, nil
	}
	if loggerOpts.MaxAge != runningCtx.LogMaxAge {
		return true, nil
	}
	if loggerOpts.MaxBackups != runningCtx.LogMaxBackups {
		return true, nil
	}
	if loggerOpts.MaxSize != runningCtx.LogMaxSize {
		return true, nil
	}
	return false, nil
}

// UpdateLogger updates the logger settings or creates a new logger, if passed nil.
func (provider *providerImpl) UpdateLogger(logger *ttlog.Logger) (*ttlog.Logger, error) {
	updateLogger, err := isLoggerChanged(logger, provider.instance)
	if err != nil {
		return logger, err
	}
	if updateLogger {
		logger.Close()
		return createLogger(provider.instance), nil
	}
	return logger, nil
}

// IsRestartable checks if the instance should be restarted in case of crash.
func (provider *providerImpl) IsRestartable() (bool, error) {
	if err := provider.updateCtx(); err != nil {
		return false, err
	}

	return provider.instance.Restartable, nil
}

// findInstSeparator returns instance separator index.
// Cartridge application uses dot and dash sepatator for the application
// and instance name (dash for stateboard, dot for others).
func findInstSeparator(inst string) int {
	sepIdx := -1
	dotIdx := strings.Index(inst, ".")
	dashIdx := strings.Index(inst, "-")

	if dotIdx+dashIdx != -2 { // Separator is found.
		mult := dotIdx * dashIdx
		if mult < 0 { // Only one separator is found.
			sepIdx = -mult
		} else {
			if dotIdx < dashIdx {
				sepIdx = dotIdx
			} else {
				sepIdx = dashIdx
			}
		}
	}

	return sepIdx
}

// getInstancesFromYML collects instances from instances.yml.
func getInstancesFromYML(dirPath string, selectedInstName string) ([]cmdcontext.RunningCtx, error) {
	instances := []cmdcontext.RunningCtx{}
	instCfgPath := path.Join(dirPath, "instances.yml")
	defAppPath := path.Join(dirPath, "init.lua")
	defAppExist := false
	if _, err := os.Stat(defAppPath); err == nil {
		defAppExist = true
	}

	ymlData, err := ioutil.ReadFile(instCfgPath)
	if err != nil {
		return nil, err
	}
	instParams := make(map[string]interface{})
	if err = yaml.Unmarshal(ymlData, instParams); err != nil {
		return nil, err
	}
	for inst, _ := range instParams {
		instance := cmdcontext.RunningCtx{}
		instance.AppName = filepath.Base(dirPath)
		instance.SingleApp = false

		sepIdx := findInstSeparator(inst)

		if sepIdx == -1 {
			instance.InstName = inst
		} else {
			instance.InstName = inst[sepIdx+1:]
		}

		if selectedInstName != "" && instance.InstName != selectedInstName {
			continue
		}

		script := path.Join(dirPath, instance.InstName+".init.lua")
		if _, err = os.Stat(script); err != nil {
			if defAppExist {
				instance.AppPath = defAppPath
			} else {
				return nil, fmt.Errorf(
					"init.lua or %s.init.lua is missing", instance.InstName,
				)
			}
		} else {
			instance.AppPath = script
		}

		instances = append(instances, instance)
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("instance(s) not found")
	}

	return instances, nil
}

// collectInstances searches all instances available in application.
func collectInstances(appName string, cliOpts *config.CliOpts,
	appDir string) ([]cmdcontext.RunningCtx, error) {
	var err error
	var appPath string

	// The user can select a specific instance from the application.
	// Example: `tt status application:server`.
	selectedInstName := ""
	colonIds := strings.Index(appName, ":")
	if colonIds != -1 {
		appNameTmp := appName
		appName = appNameTmp[:colonIds]
		selectedInstName = appNameTmp[colonIds+1:]
	}

	// We considering several scenarios:
	// 1) The application starts by `appName.lua`
	// 2) The application starts by `appName/init.lua`
	// 3) The application starts by `dirName:appName`
	// 4) Read application list from `appName/instances.yml`
	luaPath := filepath.Join(appDir, appName+".lua")
	dirPath := filepath.Join(appDir, appName)

	// Check if one or both file and/or directory exist.
	_, fileStatErr := os.Stat(luaPath)
	dirInfo, dirStatErr := os.Stat(dirPath)

	if !os.IsNotExist(fileStatErr) {
		if fileStatErr != nil {
			return nil, fileStatErr
		}
		appPath = luaPath
	} else if dirStatErr == nil && dirInfo.IsDir() {
		// Search for instances.yml
		instCfgPath := path.Join(dirPath, "instances.yml")
		if _, err = os.Stat(instCfgPath); err == nil {
			return getInstancesFromYML(dirPath, selectedInstName)
		} else {
			appPath = path.Join(dirPath, "init.lua")
			if _, err = os.Stat(appPath); err != nil {
				return nil, err
			}
		}
	} else {
		return nil, fileStatErr
	}

	return []cmdcontext.RunningCtx{
		{AppPath: appPath, AppName: appName, InstName: appName, SingleApp: true},
	}, nil
}

// cleanup removes runtime artifacts.
func cleanup(cmdCtx *cmdcontext.CmdCtx, run *cmdcontext.RunningCtx) {
	if _, err := os.Stat(run.PIDFile); err == nil {
		os.Remove(run.PIDFile)
	}

	if _, err := os.Stat(run.ConsoleSocket); err == nil {
		os.Remove(run.ConsoleSocket)
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
func createLogger(run *cmdcontext.RunningCtx) *ttlog.Logger {
	opts := ttlog.LoggerOpts{
		Filename:   run.Log,
		MaxSize:    run.LogMaxSize,
		MaxBackups: run.LogMaxBackups,
		MaxAge:     run.LogMaxAge,
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
	err = os.MkdirAll(dataDirPath, defaultDirPerms)
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

	pidAbsDir := filepath.Dir(pidFileName)
	if _, err := os.Stat(pidAbsDir); err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(pidAbsDir, defaultDirPerms)
			if err != nil {
				return fmt.Errorf(`can't crete PID file directory. Error: "%v".`, err)
			}
		} else {
			return fmt.Errorf(`can't stat PID file directory. Error: "%v".`, err)
		}
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

// makePath make application path with rules:
// * if path is not set:
//     * if single instance application: baseBath + application name.
//     * else : baseBath + application name + instance name.
// * if path is set and it is absolute:
//    * if single instance application: path + application name
//    * else: path + application name + instance name.
// * if path is set and it is relative:
//    * if single instance application: basePath + path + application name.
//    * else: basePath + path + application name + instance name.
func makePath(path string, basePath string, inst *cmdcontext.RunningCtx) string {
	res := ""

	if path == "" {
		if inst.SingleApp {
			return filepath.Join(basePath, inst.AppName)
		} else {
			res = filepath.Join(basePath, inst.AppName)
			return filepath.Join(res, inst.InstName)
		}
	}

	if filepath.IsAbs(path) {
		if inst.SingleApp {
			return filepath.Join(path, inst.AppName)
		} else {
			res = filepath.Join(path, inst.AppName)
			return filepath.Join(res, inst.InstName)
		}
	}

	res = filepath.Join(basePath, path)
	res = filepath.Join(res, inst.AppName)

	if !inst.SingleApp {
		return filepath.Join(res, inst.InstName)
	}

	return res
}

// FillCtx fills the RunningCtx context.
func FillCtx(cliOpts *config.CliOpts, cmdCtx *cmdcontext.CmdCtx,
	args []string) error {
	var err error

	if len(args) != 1 && cmdCtx.CommandName != "run" {
		if len(args) > 1 {
			return fmt.Errorf("Currently, you can specify only one instance at a time.")
		} else {
			return fmt.Errorf("Please specify the name of the application.")
		}
	}

	// All relative paths are built from the path of the tarantool.yaml file.
	// If tarantool.yaml does not exists we must return error.
	basePath := ""
	if cmdCtx.Cli.ConfigPath != "" {
		if _, err := os.Stat(cmdCtx.Cli.ConfigPath); err == nil {
			basePath = filepath.Dir(cmdCtx.Cli.ConfigPath)
		} else {
			return fmt.Errorf(`tarantool.yaml error: %s"`, err)
		}
	} else {
		return fmt.Errorf(`tarantool.yaml not found"`)
	}

	appName := args[0]
	if cmdCtx.CommandName == "run" {
		if strings.HasSuffix(appName, ".lua") {
			appName = appName[0 : len(appName)-4]
		}
	}

	instEnabledPath := ""
	if cliOpts.App != nil && cliOpts.App.InstancesAvailable != "" {
		instEnabledPath = cliOpts.App.InstancesAvailable
		if !filepath.IsAbs(instEnabledPath) {
			instEnabledPath = filepath.Join(basePath, instEnabledPath)
		}
	} else {
		instEnabledPath = basePath
	}

	instParams, err := collectInstances(appName, cliOpts, instEnabledPath)
	if err != nil {
		return fmt.Errorf("Can't find an application init file: %s", err)
	}

	// Cleanup instances list.
	cmdCtx.Running = nil

	for _, inst := range instParams {
		var running cmdcontext.RunningCtx
		var runDir string
		var logDir string
		var dataDir string

		running.AppPath = inst.AppPath
		running.AppName = inst.AppName
		running.InstName = inst.InstName

		if cliOpts.App != nil {
			runDir = cliOpts.App.RunDir
			logDir = cliOpts.App.LogDir
			dataDir = cliOpts.App.DataDir
			running.LogMaxSize = cliOpts.App.LogMaxSize
			running.LogMaxAge = cliOpts.App.LogMaxAge
			running.LogMaxBackups = cliOpts.App.LogMaxBackups
			running.Restartable = cliOpts.App.Restartable
		}

		running.RunDir = makePath(runDir, basePath, &inst)
		running.ConsoleSocket = filepath.Join(running.RunDir, running.InstName+".control")
		running.PIDFile = filepath.Join(running.RunDir, running.InstName+".pid")

		running.LogDir = makePath(logDir, basePath, &inst)
		running.Log = filepath.Join(running.LogDir, running.InstName+".log")

		running.DataDir = makePath(dataDir, basePath, &inst)

		if cmdCtx.CommandName != "run" {
			err = createDataDir(running.DataDir)
			if err != nil {
				return err
			}
		}

		cmdCtx.Running = append(cmdCtx.Running, running)
	}

	return nil
}

// Start an Instance.
func Start(cmdCtx *cmdcontext.CmdCtx, run *cmdcontext.RunningCtx) error {
	if err := createPIDFile(run.PIDFile); err != nil {
		return err
	}

	defer cleanup(cmdCtx, run)

	logger := createLogger(run)
	provider := providerImpl{cmdCtx: cmdCtx, instance: run}
	wd := NewWatchdog(run.Restartable, 5*time.Second, logger, &provider)
	wd.Start()

	return nil
}

// Stop the Instance.
func Stop(cmdCtx *cmdcontext.CmdCtx, run *cmdcontext.RunningCtx) error {
	pid, err := getPIDFromFile(run.PIDFile)
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
	if _, err := os.Stat(run.ConsoleSocket); err == nil {
		os.Remove(run.ConsoleSocket)
	}

	log.Printf("The Instance (PID = %v) has been terminated.\n", pid)

	return nil
}

// Run runs an Instance.
func Run(runOpts *RunOpts) error {
	appPath := ""
	if len(runOpts.CmdCtx.Running) != 0 {
		appPath = runOpts.CmdCtx.Running[0].AppPath
	}
	if len(runOpts.CmdCtx.Running) > 1 {
		return fmt.Errorf("specify instance name")
	}
	inst := Instance{tarantoolPath: runOpts.CmdCtx.Cli.TarantoolExecutable,
		appPath: appPath,
		env:     os.Environ()}
	err := inst.Run(runOpts.RunFlags)
	return err
}

// Status returns the status of the Instance.
func Status(cmdCtx *cmdcontext.CmdCtx, run *cmdcontext.RunningCtx) string {
	pid, err := getPIDFromFile(run.PIDFile)
	if err != nil {
		return fmt.Sprintf(InstStateStopped)
	}

	alive, err := isProcessAlive(pid)
	if !alive {
		return fmt.Sprintf(InstStateDead)
	}

	return fmt.Sprintf(InstStateRunning, pid)
}

// Logrotate rotates logs of a started tarantool instance.
func Logrotate(cmdCtx *cmdcontext.CmdCtx, run *cmdcontext.RunningCtx) (string, error) {
	pid, err := getPIDFromFile(run.PIDFile)
	if err != nil {
		return "", fmt.Errorf(InstStateStopped)
	}

	alive, err := isProcessAlive(pid)
	if !alive {
		return "", fmt.Errorf(InstStateDead)
	}

	if err := syscall.Kill(pid, syscall.Signal(syscall.SIGHUP)); err != nil {
		return "", fmt.Errorf(`Can't rotate logs: "%v".`, err)
	}

	// Rotates logs [instance name pid]
	return fmt.Sprintf("Logs has been rotated. PID: %v.", pid), nil
}

// Check returns the result of checking the syntax of the application file.
func Check(cmdCtx *cmdcontext.CmdCtx, run *cmdcontext.RunningCtx) error {
	var errbuff bytes.Buffer
	os.Setenv("TT_CLI_INSTANCE", run.AppPath)

	cmd := exec.Command(cmdCtx.Cli.TarantoolExecutable, "-e", checkSyntax)
	cmd.Stderr = &errbuff
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(errbuff.String())
	}

	return nil
}
