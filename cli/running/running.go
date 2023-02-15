package running

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v2"
)

const defaultDirPerms = 0770

var (
	instStateStopped = process_utils.ProcStateStopped
	instStateDead    = process_utils.ProcStateDead
)

// Running contains information about application instances.
type RunningCtx struct {
	// Instances contains information about application instances.
	Instances []InstanceCtx
}

// InstanceCtx contains information about application instance.
type InstanceCtx struct {
	// Path to an application.
	AppPath string
	// AppName contains the name of the application as it was passed on start.
	AppName string
	// Instance name.
	InstName string
	// Directory that stores various instance runtime artifacts like
	// console socket, PID file, etc.
	RunDir string
	// Directory that stores log files.
	LogDir string
	// Log is the name of log file.
	Log string
	// WalDir is a directory where write-ahead log (.xlog) files are stored.
	WalDir string `mapstructure:"wal_dir" yaml:"wal_dir"`
	// MemtxDir is a directory where memtx stores snapshot (.snap) files.
	MemtxDir string `mapstructure:"memtx_dir" yaml:"memtx_dir"`
	// VinylDir is a directory where vinyl files or subdirectories will be stored.
	VinylDir string `mapstructure:"vinyl_dir" yaml:"vinyl_dir"`
	// LogMaxSize is the maximum size in megabytes of the log file
	// before it gets rotated. It defaults to 100 megabytes.
	LogMaxSize int
	// LogMaxBackups is the maximum number of old log files to retain.
	// The default is to retain all old log files (though LogMaxAge may
	// still cause them to get deleted).
	LogMaxBackups int
	// LogMaxAge is the maximum number of days to retain old log files
	// based on the timestamp encoded in their filename. Note that a
	// day is defined as 24 hours and may not exactly correspond to
	// calendar days due to daylight savings, leap seconds, etc. The
	// default is not to remove old log files based on age.
	LogMaxAge int
	// The name of the file with the watchdog PID under which the
	// instance was started.
	PIDFile string
	// If the instance is started under the watchdog it should
	// restart on if it crashes.
	Restartable bool
	// Control UNIX socket for started instance.
	ConsoleSocket string
	// True if this is a single instance application (no instances.yml).
	SingleApp bool
}

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
	CmdCtx     cmdcontext.CmdCtx
	RunningCtx RunningCtx
	RunFlags   RunFlags
}

// providerImpl is an implementation of Provider interface.
type providerImpl struct {
	cmdCtx *cmdcontext.CmdCtx
	// instanceCtx is a pointer to the specific data of the instanceCtx to work with.
	instanceCtx *InstanceCtx
}

// updateCtx updates cmdCtx according to the current contents of the cfg file.
func (provider *providerImpl) updateCtx() error {
	cliOpts, _, err := configure.GetCliOpts(provider.cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	var args []string
	if provider.instanceCtx.SingleApp {
		args = []string{provider.instanceCtx.AppName}
	} else {
		args = []string{provider.instanceCtx.AppName + ":" + provider.instanceCtx.InstName}
	}

	var runningCtx RunningCtx
	if err = FillCtx(cliOpts, provider.cmdCtx, &runningCtx, args); err != nil {
		return err
	}
	provider.instanceCtx = &runningCtx.Instances[0]
	return nil
}

// createInstance reads config and creates an Instance.
func (provider *providerImpl) CreateInstance(logger *ttlog.Logger) (*Instance, error) {
	if err := provider.updateCtx(); err != nil {
		return nil, err
	}

	inst, err := NewInstance(provider.cmdCtx.Cli.TarantoolExecutable,
		provider.instanceCtx, os.Environ(), logger)
	if err != nil {
		return nil, err
	}
	return inst, nil
}

// isLoggerChanged checks if any of the logging parameters has been changed.
func isLoggerChanged(logger *ttlog.Logger, runningCtx *InstanceCtx) (bool, error) {
	if runningCtx == nil {
		return true, fmt.Errorf("runningCtx, which is used to check if the logger parameters" +
			" are updated, is nil")
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
	updateLogger, err := isLoggerChanged(logger, provider.instanceCtx)
	if err != nil {
		return logger, err
	}
	if updateLogger {
		logger.Close()
		return createLogger(provider.instanceCtx), nil
	}
	return logger, nil
}

// IsRestartable checks if the instance should be restarted in case of crash.
func (provider *providerImpl) IsRestartable() (bool, error) {
	if err := provider.updateCtx(); err != nil {
		return false, err
	}

	return provider.instanceCtx.Restartable, nil
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
func getInstancesFromYML(dirPath string, selectedInstName string) ([]InstanceCtx,
	error) {
	instances := []InstanceCtx{}
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
		instance := InstanceCtx{}
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

// CollectInstances searches all instances available in application.
func CollectInstances(appName string, appDir string) ([]InstanceCtx, error) {
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
	// If appName equals to base directory name, current working
	// directory is considered as application to work with.
	dirPath, luaPath := "", ""
	if filepath.Base(appDir) == appName {
		dirPath = appDir
	} else {
		luaPath = filepath.Join(appDir, appName+".lua")
		dirPath = filepath.Join(appDir, appName)
	}

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

	return []InstanceCtx{
		{AppPath: appPath, AppName: appName, InstName: appName, SingleApp: true},
	}, nil
}

// cleanup removes runtime artifacts.
func cleanup(run *InstanceCtx) {
	if _, err := os.Stat(run.PIDFile); err == nil {
		os.Remove(run.PIDFile)
	}

	if _, err := os.Stat(run.ConsoleSocket); err == nil {
		os.Remove(run.ConsoleSocket)
	}
}

// createLogger prepares a logger for the watchdog and instance.
func createLogger(run *InstanceCtx) *ttlog.Logger {
	opts := ttlog.LoggerOpts{
		Filename:   run.Log,
		MaxSize:    run.LogMaxSize,
		MaxBackups: run.LogMaxBackups,
		MaxAge:     run.LogMaxAge,
	}

	return ttlog.NewLogger(&opts)
}

// FillCtx fills the RunningCtx context.
func FillCtx(cliOpts *config.CliOpts, cmdCtx *cmdcontext.CmdCtx,
	runningCtx *RunningCtx, args []string) error {
	var err error

	if len(args) > 1 && cmdCtx.CommandName != "run" {
		return util.NewArgError("currently, you can specify only one instance at a time")
	}

	// All relative paths are built from the path of the tt.yaml file.
	// If tt.yaml does not exists we must return error.
	if cmdCtx.Cli.ConfigPath == "" {
		return fmt.Errorf(`%s not found`, configure.ConfigName)
	}

	instEnabledPath := cliOpts.App.InstancesEnabled
	if cliOpts.App.InstancesEnabled == "." {
		instEnabledPath = cmdCtx.Cli.ConfigDir
	}

	var appList []util.AppListEntry
	if len(args) == 0 {
		appList, err = util.CollectAppList(cmdCtx.Cli.ConfigDir, cliOpts.App.InstancesEnabled,
			true)
		if err != nil {
			return fmt.Errorf("can't collect an application list "+
				"from instances enabled path %s: %s", instEnabledPath, err)
		}
	} else {
		appList = append(appList, util.AppListEntry{Name: args[0], Location: ""})
	}

	// Cleanup instances list.
	runningCtx.Instances = nil
	for _, appInfo := range appList {
		appName := strings.TrimSuffix(appInfo.Name, ".lua")
		instances, err := CollectInstances(appName, instEnabledPath)
		if err != nil {
			return fmt.Errorf("%s: can't find an application init file: %s", appName, err)
		}

		for _, inst := range instances {
			var instance InstanceCtx
			var runDir string
			var logDir string

			instance.AppPath = inst.AppPath
			instance.AppName = inst.AppName
			instance.InstName = inst.InstName
			pathBuilder := NewArtifactsPathBuilder(cmdCtx.Cli.ConfigDir, instance.AppName).
				WithTarantoolctlLayout(cliOpts.App.TarantoolctlLayout)
			if !inst.SingleApp {
				pathBuilder = pathBuilder.ForInstance(instance.InstName)
			}

			if cliOpts.App != nil {
				runDir = cliOpts.App.RunDir
				logDir = cliOpts.App.LogDir
				instance.LogMaxSize = cliOpts.App.LogMaxSize
				instance.LogMaxAge = cliOpts.App.LogMaxAge
				instance.LogMaxBackups = cliOpts.App.LogMaxBackups
				instance.Restartable = cliOpts.App.Restartable
			}

			instance.RunDir = pathBuilder.WithPath(runDir).Make()
			instance.ConsoleSocket = filepath.Join(instance.RunDir, instance.InstName+".control")
			instance.PIDFile = filepath.Join(instance.RunDir, instance.InstName+".pid")
			instance.LogDir = pathBuilder.WithPath(logDir).Make()
			instance.Log = filepath.Join(instance.LogDir, instance.InstName+".log")
			pathBuilder = pathBuilder.WithTarantoolctlLayout(false)
			instance.WalDir = pathBuilder.WithPath(cliOpts.App.WalDir).Make()
			instance.VinylDir = pathBuilder.WithPath(cliOpts.App.VinylDir).Make()
			instance.MemtxDir = pathBuilder.WithPath(cliOpts.App.MemtxDir).Make()
			instance.SingleApp = inst.SingleApp

			if cmdCtx.CommandName == "start" || cmdCtx.CommandName == "restart" {
				for _, dataDir := range [...]string{instance.WalDir, instance.VinylDir,
					instance.MemtxDir} {
					if err = util.CreateDirectory(dataDir, defaultDirPerms); err != nil {
						return err
					}
				}
			}

			runningCtx.Instances = append(runningCtx.Instances, instance)
		}
	}

	if cmdCtx.CommandName != "connect" {
		if cmdCtx.Cli.TarantoolExecutable == "" {
			return fmt.Errorf("tarantool binary not found")
		}
	}

	return nil
}

// Start an Instance.
func Start(cmdCtx *cmdcontext.CmdCtx, run *InstanceCtx) error {
	logger := createLogger(run)
	provider := providerImpl{cmdCtx: cmdCtx, instanceCtx: run}
	preStartAction := func() error {
		if err := process_utils.CreatePIDFile(run.PIDFile); err != nil {
			return err
		}
		return nil
	}
	wd := NewWatchdog(run.Restartable, 5*time.Second, logger, &provider, preStartAction)

	defer func() {
		cleanup(run)
	}()

	wd.Start()
	return nil
}

// Stop the Instance.
func Stop(run *InstanceCtx) error {
	pid, err := process_utils.StopProcess(run.PIDFile)
	if err != nil {
		return err
	}

	// tarantool 1.10 does not have a trigger on terminate a process.
	// So the socket will be closed automatically on termination and
	// we need to delete the file.
	if _, err := os.Stat(run.ConsoleSocket); err == nil {
		os.Remove(run.ConsoleSocket)
	}

	fullInstanceName := GetAppInstanceName(*run)
	log.Infof("The Instance %s (PID = %v) has been terminated.", fullInstanceName, pid)

	return nil
}

// Run runs an Instance.
func Run(runOpts *RunOpts, scriptPath string) error {
	inst := Instance{tarantoolPath: runOpts.CmdCtx.Cli.TarantoolExecutable,
		appPath: scriptPath,
		env:     os.Environ()}
	err := inst.Run(runOpts.RunFlags)
	return err
}

// Status returns the status of the Instance.
func Status(run *InstanceCtx) process_utils.ProcessState {
	return process_utils.ProcessStatus(run.PIDFile)
}

// Logrotate rotates logs of a started tarantool instance.
func Logrotate(run *InstanceCtx) (string, error) {
	pid, err := process_utils.GetPIDFromFile(run.PIDFile)
	if err != nil {
		return "", fmt.Errorf(instStateStopped.Text)
	}

	alive, err := process_utils.IsProcessAlive(pid)
	if !alive {
		return "", fmt.Errorf(instStateDead.Text)
	}

	if err := syscall.Kill(pid, syscall.Signal(syscall.SIGHUP)); err != nil {
		return "", fmt.Errorf(`can't rotate logs: "%v"`, err)
	}

	// Rotates logs [instance name pid]
	fullInstanceName := GetAppInstanceName(*run)
	return fmt.Sprintf("%s: logs has been rotated. PID: %v.", fullInstanceName, pid), nil
}

// Check returns the result of checking the syntax of the application file.
func Check(cmdCtx *cmdcontext.CmdCtx, run *InstanceCtx) error {
	var errbuff bytes.Buffer
	os.Setenv("TT_CLI_INSTANCE", run.AppPath)

	cmd := exec.Command(cmdCtx.Cli.TarantoolExecutable, "-e", checkSyntax)
	cmd.Stderr = &errbuff
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(errbuff.String())
	}

	return nil
}

// GetAppInstanceName returns the full instance name for the passed context.
// If an application is multi-instance, the format will be AppName:InstName.
// Otherwise, the format is AppName.
func GetAppInstanceName(instance InstanceCtx) string {
	fullInstanceName := ""
	if instance.SingleApp {
		fullInstanceName = instance.AppName
	} else {
		fullInstanceName = instance.AppName + ":" + instance.InstName
	}
	return fullInstanceName
}
