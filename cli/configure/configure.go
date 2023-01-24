package configure

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/apex/log"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

const (
	ConfigName        = "tt.yaml"
	cliExecutableName = "tt"
	// systemConfigDirEnvName is an environment variable that contains a path to
	// search system config.
	systemConfigDirEnvName = "TT_SYSTEM_CONFIG_DIR"
	// instancesEnabledDirName is a default instances enabled directory name.
	InstancesEnabledDirName = "instances.enabled"
)

const (
	defaultDaemonPort    = 1024
	defaultDaemonPidFile = "tt_daemon.pid"
	defaultDaemonLogFile = "tt_daemon.log"

	daemonCfgPath     = "tt_daemon.yaml"
	configHomeEnvName = "XDG_CONFIG_HOME"
)

const (
	varPath       = "var"
	logPath       = "log"
	runPath       = "run"
	dataPath      = "lib"
	binPath       = "bin"
	includePath   = "include"
	modulesPath   = "modules"
	distfilesPath = "distfiles"
	logMaxSize    = 100
	logMaxAge     = 8
	logMaxBackups = 10
)

var (
	varDataPath = filepath.Join(varPath, dataPath)
	varLogPath  = filepath.Join(varPath, logPath)
	varRunPath  = filepath.Join(varPath, runPath)
)

var (
	// Path to default tt.yaml configuration file.
	// Defined at build time, see magefile.
	defaultConfigPath string
)

// getDefaultAppOpts generates default app config.
func getDefaultAppOpts() *config.AppOpts {
	return &config.AppOpts{
		InstancesEnabled: ".",
		RunDir:           varRunPath,
		LogDir:           varLogPath,
		LogMaxSize:       logMaxSize,
		LogMaxAge:        logMaxAge,
		LogMaxBackups:    logMaxBackups,
		Restartable:      false,
		DataDir:          varDataPath,
		BinDir:           binPath,
		IncludeDir:       includePath,
	}
}

// GetDefaultCliOpts returns `CliOpts` filled with default values.
func GetDefaultCliOpts() *config.CliOpts {
	modules := config.ModulesOpts{
		Directory: modulesPath,
	}
	ee := config.EEOpts{
		CredPath: "",
	}
	repo := config.RepoOpts{
		Rocks:   "",
		Install: distfilesPath,
	}
	templates := []config.TemplateOpts{
		{Path: "templates"},
	}
	return &config.CliOpts{
		Modules:   &modules,
		App:       getDefaultAppOpts(),
		Repo:      &repo,
		EE:        &ee,
		Templates: templates,
	}
}

// getDefaultDaemonOpts returns `DaemonOpts` filled with default values.
func getDefaultDaemonOpts() *config.DaemonOpts {
	return &config.DaemonOpts{
		Port:            defaultDaemonPort,
		PIDFile:         defaultDaemonPidFile,
		RunDir:          varRunPath,
		LogDir:          varLogPath,
		LogFile:         defaultDaemonLogFile,
		LogMaxSize:      0,
		LogMaxAge:       0,
		LogMaxBackups:   0,
		ListenInterface: "",
	}
}

// adjustPathWithConfigLocation adjust provided filePath with configDir.
// Absolute filePath is returned as is. Relative filePath is calculated relative to configDir.
// If filePath is empty, defaultDirName is appended to configDir.
func adjustPathWithConfigLocation(filePath string, configDir string,
	defaultDirName string) (string, error) {
	if filePath == "" {
		return filepath.Abs(filepath.Join(configDir, defaultDirName))
	}
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}
	return filepath.Abs(filepath.Join(configDir, filePath))
}

// resolveConfigPaths resolves all paths in config relative to specified location, and
// sets uninitialized values to defaults.
func updateCliOpts(cliOpts *config.CliOpts, configDir string) error {
	var err error

	if cliOpts.App == nil {
		cliOpts.App = getDefaultAppOpts()
	}
	if cliOpts.Repo == nil {
		cliOpts.Repo = &config.RepoOpts{
			Rocks:   "",
			Install: distfilesPath,
		}
	}
	if cliOpts.Modules == nil {
		cliOpts.Modules = &config.ModulesOpts{
			Directory: modulesPath,
		}
	}
	if cliOpts.EE == nil {
		cliOpts.EE = &config.EEOpts{
			CredPath: "",
		}
	}

	if cliOpts.App.InstancesEnabled == "" {
		cliOpts.App.InstancesEnabled = "."
	} else if cliOpts.App.InstancesEnabled != "." {
		if cliOpts.App.InstancesEnabled, err =
			adjustPathWithConfigLocation(cliOpts.App.InstancesEnabled, configDir, ""); err != nil {
			return err
		}
	}
	if cliOpts.App.RunDir, err = adjustPathWithConfigLocation(cliOpts.App.RunDir,
		configDir, varRunPath); err != nil {
		return err
	}
	if cliOpts.App.LogDir, err = adjustPathWithConfigLocation(cliOpts.App.LogDir,
		configDir, varLogPath); err != nil {
		return err
	}
	if cliOpts.App.DataDir, err = adjustPathWithConfigLocation(cliOpts.App.DataDir,
		configDir, varDataPath); err != nil {
		return err
	}
	if cliOpts.App.BinDir, err = adjustPathWithConfigLocation(cliOpts.App.BinDir,
		configDir, binPath); err != nil {
		return err
	}
	if cliOpts.App.IncludeDir, err = adjustPathWithConfigLocation(cliOpts.App.IncludeDir,
		configDir, includePath); err != nil {
		return err
	}
	if cliOpts.Repo.Install, err = adjustPathWithConfigLocation(cliOpts.Repo.Install,
		configDir, distfilesPath); err != nil {
		return err
	}

	if cliOpts.Modules != nil {
		if cliOpts.Modules.Directory, err = adjustPathWithConfigLocation(cliOpts.Modules.Directory,
			configDir, modulesPath); err != nil {
			return err
		}
	}

	for i := range cliOpts.Templates {
		if cliOpts.Templates[i].Path, err = adjustPathWithConfigLocation(
			cliOpts.Templates[i].Path, configDir, "."); err != nil {
			return err
		}
	}

	if cliOpts.App.LogMaxAge == 0 {
		cliOpts.App.LogMaxAge = logMaxAge
	}
	if cliOpts.App.LogMaxSize == 0 {
		cliOpts.App.LogMaxSize = logMaxSize
	}
	if cliOpts.App.LogMaxBackups == 0 {
		cliOpts.App.LogMaxBackups = logMaxBackups
	}

	return nil
}

// GetCliOpts returns Tarantool CLI options from the config file
// located at path configurePath.
func GetCliOpts(configurePath string) (*config.CliOpts, string, error) {
	var cfg config.Config
	// Config could not be processed.
	configPath, err := util.GetYamlFileName(configurePath, true)
	if err == nil {
		// Config file is found, load it.
		rawConfigOpts, err := util.ParseYAML(configPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to parse Tarantool CLI configuration: %s", err)
		}

		if err := mapstructure.Decode(rawConfigOpts, &cfg); err != nil {
			return nil, "", fmt.Errorf("failed to parse Tarantool CLI configuration: %s", err)
		}

		if cfg.CliConfig == nil {
			return nil, "",
				fmt.Errorf("failed to parse Tarantool CLI configuration: missing tt section")
		}
	} else if err != nil && !os.IsNotExist(err) {
		// TODO: Add warning in next patches, discussion
		// what if the file exists, but access is denied, etc.
		return nil, "", fmt.Errorf("failed to get access to configuration file: %s", err)
	} else if os.IsNotExist(err) {
		cfg.CliConfig = GetDefaultCliOpts()
		configPath = ""
	}

	configDir := ""
	if configPath == "" {
		configDir, err = os.Getwd()
		if err != nil {
			return cfg.CliConfig, configPath, err
		}
	} else {
		if configDir, err = filepath.Abs(filepath.Dir(configPath)); err != nil {
			return cfg.CliConfig, configPath, err
		}
	}

	if err = updateCliOpts(cfg.CliConfig, configDir); err != nil {
		return cfg.CliConfig, "", err
	}

	return cfg.CliConfig, configPath, nil
}

// GetDaemonOpts returns tt daemon options from the config file
// located at path configurePath.
func GetDaemonOpts(configurePath string) (*config.DaemonOpts, error) {
	var cfg config.DaemonCfg

	if configurePath == "" {
		return getDefaultDaemonOpts(), nil
	}

	// Config could not be processed.
	if _, err := os.Stat(configurePath); err != nil {
		return nil, fmt.Errorf("failed to get access to daemon configuration file: %s", err)
	}

	rawConfigOpts, err := util.ParseYAML(configurePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse daemon configuration: %s", err)
	}

	if err := mapstructure.Decode(rawConfigOpts, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse daemon configuration: %s", err)
	}

	if cfg.DaemonConfig == nil {
		return nil, fmt.Errorf("failed to parse daemon configuration: missing daemon section")
	}

	if cfg.DaemonConfig.PIDFile == "" {
		cfg.DaemonConfig.PIDFile = defaultDaemonPidFile
	}

	if cfg.DaemonConfig.LogFile == "" {
		cfg.DaemonConfig.LogFile = defaultDaemonLogFile
	}

	if cfg.DaemonConfig.Port == 0 {
		cfg.DaemonConfig.Port = defaultDaemonPort
	}

	if cfg.DaemonConfig.RunDir == "" {
		cfg.DaemonConfig.RunDir = filepath.Join(filepath.Dir(configurePath),
			varRunPath)
	}
	if cfg.DaemonConfig.LogDir == "" {
		cfg.DaemonConfig.LogDir = filepath.Join(filepath.Dir(configurePath),
			varLogPath)
	}

	return cfg.DaemonConfig, nil
}

// ValidateCliOpts checks for ambiguous config options.
func ValidateCliOpts(cliCtx *cmdcontext.CliCtx) error {
	if cliCtx.LocalLaunchDir != "" {
		if cliCtx.IsSystem {
			return fmt.Errorf("you can specify only one of -L(--local) and -S(--system) options")
		}
		if cliCtx.ConfigPath != "" {
			return fmt.Errorf("you can specify only one of -L(--local) and -с(--cfg) options")
		}
	} else {
		if cliCtx.IsSystem && cliCtx.ConfigPath != "" {
			return fmt.Errorf("you can specify only one of -S(--system) and -с(--cfg) options")
		}
	}
	return nil
}

// Cli performs initial CLI configuration.
func Cli(cmdCtx *cmdcontext.CmdCtx) error {
	if cmdCtx.Cli.Verbose {
		log.SetLevel(log.DebugLevel)
	}

	if cmdCtx.Cli.ConfigPath != "" {
		if _, err := os.Stat(cmdCtx.Cli.ConfigPath); err != nil {
			return fmt.Errorf("specified path to the configuration file is invalid: %s", err)
		}
	}

	var err error
	cmdCtx.Cli.DaemonCfgPath, err = getDaemonCfgPath(daemonCfgPath)
	if err != nil {
		return fmt.Errorf("failed to get tt daemon config: %s", err)
	}

	// Set default (system) tarantool binary, can be replaced by "local" or "system" later.
	cmdCtx.Cli.TarantoolExecutable, _ = exec.LookPath("tarantool")

	switch {
	case cmdCtx.Cli.IsSystem:
		return configureSystemCli(cmdCtx)
	case cmdCtx.Cli.LocalLaunchDir != "":
		return configureLocalLaunch(cmdCtx)
	}

	// No flags specified.
	return configureDefaultCli(cmdCtx)
}

// ExternalCmd configures external commands.
func ExternalCmd(rootCmd *cobra.Command, cmdCtx *cmdcontext.CmdCtx,
	modulesInfo *modules.ModulesInfo, args []string) {
	configureExistsCmd(rootCmd, modulesInfo)
	configureNonExistentCmd(rootCmd, cmdCtx, modulesInfo, args)
}

// configureExistsCmd configures an external commands
// that have internal implementation.
func configureExistsCmd(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) {
	for _, cmd := range rootCmd.Commands() {
		if module, ok := (*modulesInfo)[cmd.CommandPath()]; ok {
			if !module.IsInternal {
				cmd.DisableFlagParsing = true
			}
		}
	}
}

// configureNonExistentCmd configures an external command that
// has no internal implementation within the Tarantool CLI.
func configureNonExistentCmd(rootCmd *cobra.Command, cmdCtx *cmdcontext.CmdCtx,
	modulesInfo *modules.ModulesInfo, args []string) {
	// Since the user can pass flags, to determine the name of
	// an external command we have to take the first non-flag argument.
	externalCmd := args[0]
	for _, name := range args {
		if !strings.HasPrefix(name, "-") && name != "help" {
			externalCmd = name
			break
		}
	}

	// We avoid overwriting existing commands - we should add a command only
	// if it doesn't have an internal implementation in Tarantool CLI.
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == externalCmd {
			return
		}
	}

	helpCmd := util.GetHelpCommand(rootCmd)
	externalCmdPath := rootCmd.Name() + " " + externalCmd
	if module, ok := (*modulesInfo)[externalCmdPath]; ok {
		if !module.IsInternal {
			rootCmd.AddCommand(newExternalCommand(cmdCtx, modulesInfo, externalCmd,
				externalCmdPath, nil))
			helpCmd.AddCommand(newExternalCommand(cmdCtx, modulesInfo, externalCmd, externalCmdPath,
				[]string{"--help"}))
		}
	}
}

// newExternalCommand returns a pointer to a new external
// command that will call modules.RunCmd.
func newExternalCommand(cmdCtx *cmdcontext.CmdCtx, modulesInfo *modules.ModulesInfo,
	cmdName, cmdPath string, addArgs []string) *cobra.Command {
	cmd := &cobra.Command{
		Use: cmdName,
		Run: func(cmd *cobra.Command, args []string) {
			if addArgs != nil {
				args = append(args, addArgs...)
			}

			cmdCtx.Cli.ForceInternal = false
			if err := modules.RunCmd(cmdCtx, cmdPath, modulesInfo, nil, args); err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	cmd.DisableFlagParsing = true
	return cmd
}

// detectLocalTarantool searches for available Tarantool executable.
func detectLocalTarantool(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts) error {
	localTarantool, err := util.JoinAbspath(cliOpts.App.BinDir, "tarantool")
	if err != nil {
		return err
	}

	if _, err := os.Stat(localTarantool); err == nil {
		if _, err := exec.LookPath(localTarantool); err != nil {
			return fmt.Errorf(`found Tarantool binary '%s' isn't executable: %s`,
				localTarantool, err)
		}

		cmdCtx.Cli.TarantoolExecutable = localTarantool
		cmdCtx.Cli.IsTarantoolBinFromRepo = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to get access to Tarantool binary file: %s", err)
	}

	log.Debugf("Tarantool executable found: '%s'", cmdCtx.Cli.TarantoolExecutable)

	return nil
}

// detectLocalTt searches for available TT executable.
func detectLocalTt(cliOpts *config.CliOpts) (string, error) {
	localCli, err := util.JoinAbspath(cliOpts.App.BinDir, cliExecutableName)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(localCli); err == nil {
		localCli, err = filepath.EvalSymlinks(localCli)
		if err != nil {
			return "", err
		}
	} else if os.IsNotExist(err) {
		return "", nil
	}

	log.Debugf("TT executable found: '%s'", localCli)

	return localCli, nil
}

// configureLocalCli configures Tarantool CLI if the launch is local.
func configureLocalCli(cmdCtx *cmdcontext.CmdCtx) error {
	launchDir, err := os.Getwd()
	if err != nil {
		return err
	}

	if cmdCtx.Cli.ConfigPath == "" {
		cmdCtx.Cli.ConfigPath, err = util.GetYamlFileName(filepath.Join(launchDir, ConfigName),
			true)
		if err != nil {
			// TODO: Add warning messages, discussion what if the file
			// exists, but access is denied, etc.
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to get access to configuration file: %s", err)
			}
			if cmdCtx.Cli.ConfigPath, err = getConfigPath(ConfigName); err != nil {
				return fmt.Errorf("failed to get Tarantool CLI config: %s", err)
			}
			if cmdCtx.Cli.ConfigPath == "" {
				if cmdCtx.Cli.LocalLaunchDir != "" {
					return fmt.Errorf("failed to find Tarantool CLI config for '%s'",
						cmdCtx.Cli.LocalLaunchDir)
				}
				cmdCtx.Cli.ConfigPath = getSystemConfigPath()
			}
		}
	}

	cliOpts, _, err := GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	if cliOpts.App == nil || cliOpts.App.BinDir == "" {
		// No bid_dir specified.
		return nil
	}

	if err = detectLocalTarantool(cmdCtx, cliOpts); err != nil {
		return err
	}

	localCli, err := detectLocalTt(cliOpts)
	if err != nil {
		return err
	}

	// We have to use the absolute path to the current binary "tt" for
	// comparison with "localCli", because the same binary can be started
	// using a relative path, and we don't want to execute "exec" in this case.
	currentCli, err := os.Executable()
	if err != nil {
		return err
	}
	currentCli, err = filepath.EvalSymlinks(currentCli)
	if err != nil {
		return err
	}

	// This should save us from exec looping.
	if localCli != "" && localCli != currentCli {
		if _, err := os.Stat(localCli); err == nil {
			if _, err := exec.LookPath(localCli); err != nil {
				return fmt.Errorf(
					`found tt binary in local directory "%s" isn't executable: %s`, launchDir, err)
			}

			// We are not using the "RunExec" function because we have no reason to have several
			// "tt" processes. Moreover, it looks strange when we start "tt", which starts "tt",
			// which starts tarantool or some external module.
			err = syscall.Exec(localCli, append([]string{localCli},
				excludeArgumentsForChildTt(os.Args[1:])...), os.Environ())
			if err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to get access to tt binary file: %s", err)
		}
	}

	return nil
}

// configureLocalLaunch configures the context using the specified local launch path.
func configureLocalLaunch(cmdCtx *cmdcontext.CmdCtx) error {
	var err error
	launchDir := ""
	if cmdCtx.Cli.LocalLaunchDir != "" {
		if launchDir, err = filepath.Abs(cmdCtx.Cli.LocalLaunchDir); err != nil {
			return fmt.Errorf(`failed to get absolute path to local directory: %s`, err)
		}

		log.Debugf("Local launch directory: %s", launchDir)

		if _, err = util.Chdir(launchDir); err != nil {
			return fmt.Errorf(`failed to change working directory: %s`, err)
		}
	}

	return configureLocalCli(cmdCtx)
}

// excludeArgumentsForChildTt removes arguments that are not required for child tt.
func excludeArgumentsForChildTt(args []string) []string {
	filteredArgs := []string{}
	skip := 0
	for _, arg := range args {
		if skip > 0 {
			skip = skip - 1
			continue
		}
		switch arg {
		case "-L", "--local":
			skip = 1
			continue
		}
		filteredArgs = append(filteredArgs, arg)
	}
	return filteredArgs
}

// getSystemConfigPath returns system config path.
func getSystemConfigPath() string {
	if configPathFromEnv := os.Getenv(systemConfigDirEnvName); configPathFromEnv != "" {
		return filepath.Join(configPathFromEnv, ConfigName)
	} else {
		return filepath.Join(defaultConfigPath, ConfigName)
	}
}

// configureSystemCli configures Tarantool CLI if the launch is system.
func configureSystemCli(cmdCtx *cmdcontext.CmdCtx) error {
	if cmdCtx.Cli.ConfigPath == "" {
		cmdCtx.Cli.ConfigPath = getSystemConfigPath()
	}

	return configureLocalCli(cmdCtx)
}

// configureDefaultCLI configures Tarantool CLI if the launch was without flags (-S or -L).
func configureDefaultCli(cmdCtx *cmdcontext.CmdCtx) error {
	var err error

	// It may be fine that the system tarantool is absent because:
	// 1) We use the "local" tarantool.
	// 2) For our purpose, tarantool is not needed at all.
	if err != nil {
		log.Info("Can't set the default tarantool from the system")
	}

	// If neither the local start nor the system flag is specified,
	// we ourselves determine what kind of launch it is.

	if cmdCtx.Cli.ConfigPath == "" {
		// We start looking for config in the current directory, going down to root directory.
		// If the config is found, we assume that it is a local launch in this directory.
		// If the config is not found, then we take it from the standard place (/etc/tarantool).

		if cmdCtx.Cli.ConfigPath, err = getConfigPath(ConfigName); err != nil {
			return fmt.Errorf("failed to get Tarantool CLI config: %s", err)
		}
	}

	if cmdCtx.Cli.ConfigPath != "" {
		return configureLocalCli(cmdCtx)
	}

	return configureSystemCli(cmdCtx)
}

// getConfigPath looks for the path to the tt.yaml configuration file,
// looking through all directories from the current one to the root.
// This search pattern is chosen for the convenience of the user.
func getConfigPath(configName string) (string, error) {
	curDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to detect current directory: %s", err)
	}

	for curDir != "/" {
		configPath, err := util.GetYamlFileName(filepath.Join(curDir, ConfigName), true)
		if err == nil {
			return configPath, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		curDir = filepath.Dir(curDir)
	}

	return "", nil
}

// getDaemonCfgPath looks for the path to the tt_daemon.yaml configuration file.
// Tries to locate tt_daemon.cfg in following order:
// 1) $XDG_CONFIG_HOME/tt/tt_daemon.cfg (if $XDG_CONFIG_HOME is not set,
// uses $HOME/.config);
// 2) $HOME/.tt_daemon.cfg;
// 3) looking in current directory for tt_daemon.cfg.
// See: https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html,
// https://unix.stackexchange.com/posts/313001/revisions.
func getDaemonCfgPath(configName string) (string, error) {
	var xdgConfigDir string
	xdgConfigHome := os.Getenv(configHomeEnvName)
	homeDir := os.Getenv("HOME")

	if xdgConfigHome != "" {
		xdgConfigDir = fmt.Sprintf("%s/tt", xdgConfigHome)
	} else {
		xdgConfigDir = fmt.Sprintf("%s/.config/tt", homeDir)
	}

	// Config in $XDG_CONFIG_HOME.
	configPath := fmt.Sprintf("%s/%s", xdgConfigDir, configName)
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	// Config in $HOME.
	configPath = fmt.Sprintf("%s/.%s", homeDir, configName)
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	// Config in current dir.
	curDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to detect current directory: %s", err)
	}

	configPath = fmt.Sprintf("%s/%s", curDir, configName)
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	}

	return "", nil
}
