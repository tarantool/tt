package running

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running/internal/layout"
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/util/regexputil"
)

const defaultDirPerms = 0770

// stateBoardInstName is cartridge stateboard instance name.
const stateBoardInstName = "stateboard"

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
	// AppDir is an application directory.
	AppDir string
	// InstanceScript is a script to run if any.
	InstanceScript string
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
	WalDir string
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
	// ClusterConfigPath is a path of cluster configuration.
	ClusterConfigPath string
	// Configuration is instance configuration loaded from cluster config.
	Configuration cluster.InstanceConfig
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
		args = []string{provider.instanceCtx.AppName + string(InstanceDelimiter) +
			provider.instanceCtx.InstName}
	}

	var runningCtx RunningCtx
	if err = FillCtx(cliOpts, provider.cmdCtx, &runningCtx, args); err != nil {
		return err
	}
	provider.instanceCtx = &runningCtx.Instances[0]
	return nil
}

// createInstance reads config and creates an Instance.
func (provider *providerImpl) CreateInstance(logger *ttlog.Logger) (inst Instance, err error) {
	if err = provider.updateCtx(); err != nil {
		return
	}

	if provider.instanceCtx.ClusterConfigPath != "" {
		logger.Printf("Watchdog(INFO): using %q cluster config for instance %q",
			provider.instanceCtx.ClusterConfigPath,
			provider.instanceCtx.InstName,
		)
		return newClusterInstance(provider.cmdCtx.Cli.TarantoolCli, *provider.instanceCtx, logger)
	}
	return newScriptInstance(provider.cmdCtx.Cli.TarantoolCli.Executable, *provider.instanceCtx,
		logger)
}

// isLoggerChanged checks if any of the logging parameters has been changed.
func isLoggerChanged(logger *ttlog.Logger, instanceCtx *InstanceCtx) (bool, error) {
	if logger == nil {
		return true, nil
	}
	if instanceCtx == nil {
		return true, fmt.Errorf("logger changed check failed: passing null as an instance context")
	}
	loggerOpts := logger.GetOpts()

	// Check if some of the parameters have been changed.
	if loggerOpts.Filename != instanceCtx.Log {
		return true, nil
	}
	if loggerOpts.MaxAge != instanceCtx.LogMaxAge {
		return true, nil
	}
	if loggerOpts.MaxBackups != instanceCtx.LogMaxBackups {
		return true, nil
	}
	if loggerOpts.MaxSize != instanceCtx.LogMaxSize {
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

// searchApplicationScript searches for application script in a directory.
func searchApplicationScript(applicationsDir string, appName string) (InstanceCtx, error) {
	instCtx := InstanceCtx{AppName: appName, InstName: appName, SingleApp: true,
		AppDir: util.JoinPaths(applicationsDir, appName)}

	luaPath := filepath.Join(applicationsDir, appName+".lua")
	if _, err := os.Stat(luaPath); err != nil {
		if os.IsNotExist(err) {
			return instCtx, nil
		} else {
			return instCtx, err
		}
	}

	instCtx.InstanceScript = luaPath
	return instCtx, nil
}

// appDirCtx describes important files in application directory.
type appDirCtx struct {
	// defaultLuaPath - path to the default lua script.
	defaultLuaPath string
	// clusterCfgPath is a cluster config file path.
	clusterCfgPath string
	// instCfgPath instances configuration file path.
	instCfgPath string
}

// collectAppDirFiles searches for config files and default instance script.
func collectAppDirFiles(appDir string) (appDirCtx appDirCtx, err error) {
	appDirCtx.defaultLuaPath = filepath.Join(appDir, "init.lua")
	if _, err = os.Stat(appDirCtx.defaultLuaPath); err != nil && !os.IsNotExist(err) {
		return
	} else if os.IsNotExist(err) {
		appDirCtx.defaultLuaPath = ""
	}

	if appDirCtx.clusterCfgPath, err = util.GetYamlFileName(
		filepath.Join(appDir, "config.yml"), false); err != nil {
		return
	}

	if appDirCtx.instCfgPath, err = util.GetYamlFileName(
		filepath.Join(appDir, "instances.yml"), false); err != nil {
		return
	}

	if appDirCtx.instCfgPath == "" {
		if appDirCtx.clusterCfgPath != "" {
			// Cluster config will work only if instances.yml exists nearby.
			err = fmt.Errorf(
				"cluster config %q is found, but instances config (instances.yml) is missing",
				appDirCtx.clusterCfgPath)
		} else {
			if appDirCtx.defaultLuaPath == "" {
				err = fmt.Errorf("require files are missing in application directory %q: "+
					"there must be instances config or the default instance script (%q)",
					appDir, "init.lua")
			}
		}
	}
	return
}

// getInstanceName gets instance name from app name + instance name.
func getInstanceName(fullInstanceName string, isClusterInstance bool) string {
	if isClusterInstance {
		// If we have a cluster instance, delimiters are ignored.
		return fullInstanceName
	}
	// Consider `-stateboard` suffix for the cartridge application compatibility.
	if strings.HasSuffix(fullInstanceName, fmt.Sprintf("-%s", stateBoardInstName)) {
		return stateBoardInstName
	}

	sepIndex := strings.Index(fullInstanceName, ".")
	if sepIndex == -1 {
		return fullInstanceName
	}
	return fullInstanceName[sepIndex+1:]
}

// findInstanceScriptInAppDir searches for instance script.
func findInstanceScriptInAppDir(appDir, instName, clusterCfgPath, defaultScript string) (
	string, error) {
	if clusterCfgPath != "" {
		// TODO: add searching for app: file: script from instance config.
		return "", nil
	}
	script := filepath.Join(appDir, instName+".init.lua")
	if _, err := os.Stat(script); err != nil {
		if defaultScript != "" {
			return defaultScript, nil
		} else {
			return "", fmt.Errorf("init.lua or %s.init.lua is missing", instName)
		}
	}
	return script, nil
}

// loadInstanceConfig load instance configuration from cluster config.
func loadInstanceConfig(configPath, instName string) (cluster.InstanceConfig, error) {
	var instCfg cluster.InstanceConfig
	if configPath == "" {
		return instCfg, nil
	}
	clusterCfg, err := cluster.GetClusterConfig(configPath)
	if err != nil {
		return instCfg, err
	}
	if instCfg, err = cluster.GetInstanceConfig(clusterCfg, instName); err != nil {
		return instCfg, err
	}
	return instCfg, nil
}

// collectInstancesFromAppDir collects instances information from application directory.
func collectInstancesFromAppDir(appDir string, selectedInstName string) (
	[]InstanceCtx,
	error,
) {
	instances := []InstanceCtx{}
	if !util.IsDir(appDir) {
		return instances, fmt.Errorf("%q doesn't exist or not a directory", appDir)
	}

	appDirFiles, err := collectAppDirFiles(appDir)
	if err != nil {
		return instances, err
	}

	if appDirFiles.instCfgPath == "" {
		if appDirFiles.defaultLuaPath != "" {
			return []InstanceCtx{{
				InstanceScript: appDirFiles.defaultLuaPath,
				AppName:        filepath.Base(appDir),
				InstName:       filepath.Base(appDir),
				AppDir:         appDir,
				SingleApp:      true}}, nil
		}
	}

	instParams, err := util.ParseYAML(appDirFiles.instCfgPath)
	if err != nil {
		return nil, err
	}
	for inst := range instParams {
		instance := InstanceCtx{AppDir: appDir, ClusterConfigPath: appDirFiles.clusterCfgPath}
		instance.InstName = getInstanceName(inst, instance.ClusterConfigPath != "")
		if selectedInstName != "" && instance.InstName != selectedInstName {
			continue
		}

		if instance.Configuration, err = loadInstanceConfig(instance.ClusterConfigPath,
			instance.InstName); err != nil {
			return instances, err
		}

		instance.AppName = filepath.Base(appDir)
		instance.SingleApp = false
		if instance.InstanceScript, err = findInstanceScriptInAppDir(appDir, instance.InstName,
			appDirFiles.clusterCfgPath, appDirFiles.defaultLuaPath); err != nil {
			return instances, err
		}
		instances = append(instances, instance)
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("instance(s) not found")
	}

	return instances, nil
}

// CollectInstances searches all instances available in application.
func CollectInstances(appName string, applicationsDir string) ([]InstanceCtx, error) {
	// The user can select a specific instance from the application.
	// Example: `tt status application:server`.
	selectedInstName := ""
	colonIds := strings.Index(appName, string(InstanceDelimiter))
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
	if instCtx, err := searchApplicationScript(applicationsDir, appName); err != nil ||
		instCtx.InstanceScript != "" {
		return []InstanceCtx{instCtx}, err
	}

	appDir := filepath.Join(applicationsDir, appName)
	if filepath.Base(applicationsDir) == appName {
		appDir = applicationsDir
	}

	return collectInstancesFromAppDir(appDir, selectedInstName)
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

// configMap is a helper structure to bind cluster config path with a pointer to value storage.
type configMap[T any] struct {
	// path is a path to the value to get from config.
	path []string
	// destination is destination pointer for storing the value.
	destination *T
}

// mapValuesFromConfig get values specified by paths from cfg config and stores them by pointers
// and modifying with mapFunc.
func mapValuesFromConfig[T any](cfg *cluster.Config, mapFunc func(val T) (T, error),
	maps ...configMap[T]) error {
	for _, cfgMapping := range maps {
		value, err := cfg.Get(cfgMapping.path)
		if err != nil {
			var eNotExist *cluster.NotExistError
			if errors.As(err, &eNotExist) {
				continue
			} else {
				return err
			}
		}
		castedValue, ok := value.(T)
		if !ok {
			return fmt.Errorf("cannot get config value at %q as %T", cfgMapping.path, *new(T))
		}
		newValue, err := mapFunc(castedValue)
		if err != nil {
			return err
		}
		*cfgMapping.destination = newValue
	}
	return nil
}

// setInstCtxFromTtConfig sets instance context members from tt config.
func setInstCtxFromTtConfig(inst *InstanceCtx, cliOpts *config.CliOpts, ttConfigDir string) error {
	tarantoolCtlLayout := false
	if cliOpts.Env != nil {
		inst.LogMaxSize = cliOpts.Env.LogMaxSize
		inst.LogMaxAge = cliOpts.Env.LogMaxAge
		inst.LogMaxBackups = cliOpts.Env.LogMaxBackups
		inst.Restartable = cliOpts.Env.Restartable
		tarantoolCtlLayout = cliOpts.Env.TarantoolctlLayout
	}
	if cliOpts.App != nil {
		var envLayout layout.Layout = nil
		var err error
		if tarantoolCtlLayout && inst.SingleApp {
			// Tarantoolctl layout is still relative to the configuration file location.
			envLayout, err = layout.NewTntCtlLayout(ttConfigDir, inst.AppName)
		} else {
			envLayout, err = layout.NewMultiInstLayout(inst.AppDir, inst.AppName, inst.InstName)
		}
		if err != nil {
			return err
		}

		inst.ConsoleSocket = envLayout.ConsoleSocket(cliOpts.App.RunDir)
		inst.PIDFile = envLayout.PidFile(cliOpts.App.RunDir)
		inst.RunDir = filepath.Dir(inst.ConsoleSocket)

		inst.Log = envLayout.LogFile(cliOpts.App.LogDir)
		inst.LogDir = filepath.Dir(inst.Log)

		inst.WalDir = envLayout.DataDir(cliOpts.App.WalDir)
		inst.VinylDir = envLayout.DataDir(cliOpts.App.VinylDir)
		inst.MemtxDir = envLayout.DataDir(cliOpts.App.MemtxDir)
	}
	return nil
}

// setInstCtxFromClusterConfig set instance context values from loaded configuration.
func setInstCtxFromClusterConfig(instance *InstanceCtx) error {
	if instance.Configuration.RawConfig != nil {
		return mapValuesFromConfig(instance.Configuration.RawConfig,
			func(val string) (string, error) {
				return util.JoinAbspath(instance.AppDir, val)
			},
			configMap[string]{[]string{"wal", "dir"}, &instance.WalDir},
			configMap[string]{[]string{"vinyl", "dir"}, &instance.VinylDir},
			configMap[string]{[]string{"snapshot", "dir"}, &instance.MemtxDir},
			configMap[string]{[]string{"console", "socket"}, &instance.ConsoleSocket})

	}
	return nil
}

// renderInstCtxMembers instantiates some members of instance context.
func renderInstCtxMembers(instance *InstanceCtx) error {
	templateData := map[string]string{
		"instance_name": instance.InstName,
	}
	for _, dstString := range []*string{
		&instance.WalDir, &instance.VinylDir, &instance.MemtxDir, &instance.ConsoleSocket,
	} {
		renderedString, err := regexputil.ApplyVars(*dstString, templateData)
		if err != nil {
			return fmt.Errorf("error instantiating template: %w", err)
		}
		*dstString = renderedString
	}
	return nil
}

// CollectInstancesForApps collects instances information for applications in list.
func CollectInstancesForApps(appList []util.AppListEntry, cliOpts *config.CliOpts,
	ttConfigDir string) (
	[]InstanceCtx, error) {
	instEnabledPath := cliOpts.Env.InstancesEnabled
	if cliOpts.Env.InstancesEnabled == "." {
		instEnabledPath = ttConfigDir
	}

	var instances []InstanceCtx
	for _, appInfo := range appList {
		appName := strings.TrimSuffix(appInfo.Name, ".lua")
		collectedInstances, err := CollectInstances(appName, instEnabledPath)
		if err != nil {
			return instances, fmt.Errorf("%s: can't find an application init file: %s",
				appName, err)
		}

		for _, inst := range collectedInstances {
			var instance = inst

			if err = setInstCtxFromTtConfig(&instance, cliOpts, ttConfigDir); err != nil {
				return instances, err
			}

			if err = setInstCtxFromClusterConfig(&instance); err != nil {
				return instances, err
			}

			if err = renderInstCtxMembers(&instance); err != nil {
				return instances, err
			}

			instances = append(instances, instance)
		}
	}
	return instances, nil
}

// createInstanceDataDirectories creates directories for data and runtime artifacts.
func createInstanceDataDirectories(instance InstanceCtx) error {
	for _, dataDir := range [...]string{instance.WalDir, instance.VinylDir,
		instance.MemtxDir, instance.RunDir, instance.LogDir} {
		if err := util.CreateDirectory(dataDir, defaultDirPerms); err != nil {
			return err
		}
	}
	return nil
}

// FillCtx fills the RunningCtx context.
func FillCtx(cliOpts *config.CliOpts, cmdCtx *cmdcontext.CmdCtx,
	runningCtx *RunningCtx, args []string) error {
	var err error

	if len(args) > 1 && cmdCtx.CommandName != "run" && cmdCtx.CommandName != "connect" {
		return util.NewArgError("currently, you can specify only one instance at a time")
	}

	// All relative paths are built from the path of the tt.yaml file.
	// If tt.yaml does not exists we must return error.
	if cmdCtx.Cli.ConfigPath == "" {
		return fmt.Errorf(`%s not found`, configure.ConfigName)
	}

	var appList []util.AppListEntry
	if len(args) == 0 {
		appList, err = util.CollectAppList(cmdCtx.Cli.ConfigDir, cliOpts.Env.InstancesEnabled,
			true)
		if err != nil {
			return fmt.Errorf("can't collect an application list "+
				"from instances enabled path %s: %s", cliOpts.Env.InstancesEnabled, err)
		}
	} else {
		appList = append(appList, util.AppListEntry{Name: args[0], Location: ""})
	}

	if runningCtx.Instances, err = CollectInstancesForApps(appList, cliOpts,
		cmdCtx.Cli.ConfigDir); err != nil {
		return err
	}

	return nil
}

// Start an Instance.
func Start(cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx) error {
	if err := createInstanceDataDirectories(*inst); err != nil {
		return err
	}

	logger := createLogger(inst)
	provider := providerImpl{cmdCtx: cmdCtx, instanceCtx: inst}
	preStartAction := func() error {
		if err := process_utils.CreatePIDFile(inst.PIDFile); err != nil {
			return err
		}
		return nil
	}
	wd := NewWatchdog(inst.Restartable, 5*time.Second, logger, &provider, preStartAction)

	defer func() {
		cleanup(inst)
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
	inst := scriptInstance{tarantoolPath: runOpts.CmdCtx.Cli.TarantoolCli.Executable,
		appPath: scriptPath}
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
		return "", fmt.Errorf(instStateStopped.String())
	}

	alive, err := process_utils.IsProcessAlive(pid)
	if !alive {
		return "", fmt.Errorf(instStateDead.String())
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
	os.Setenv("TT_CLI_INSTANCE", run.InstanceScript)

	cmd := exec.Command(cmdCtx.Cli.TarantoolCli.Executable, "-e", checkSyntax)
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
		fullInstanceName = instance.AppName + string(InstanceDelimiter) + instance.InstName
	}
	return fullInstanceName
}

// IsAbleToStartInstances checks if it is possible to start instances.
func IsAbleToStartInstances(instances []InstanceCtx, cmdCtx *cmdcontext.CmdCtx) (
	bool, string) {
	tntVersion, err := cmdCtx.Cli.TarantoolCli.GetVersion()
	if err != nil {
		return false, err.Error()
	}
	for _, inst := range instances {
		if inst.ClusterConfigPath != "" {
			if tntVersion.Major < 3 {
				return false, fmt.Sprintf(
					`cluster config is supported by Tarantool starting from version 3.0.
Current Tarantool version: %s
Cluster config path: %q`, tntVersion.Str, inst.ClusterConfigPath)
			}
		}
	}
	return true, ""
}
