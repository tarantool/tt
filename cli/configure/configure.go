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
	ConfigName        = "tarantool.yaml"
	cliExecutableName = "tt"
	// systemConfigDirEnvName is an environment variable that contains a path to
	// search system config.
	systemConfigDirEnvName = "TT_SYSTEM_CONFIG_DIR"
	// instancesEnabledDirName is a default instances enabled directory name.
	InstancesEnabledDirName = "instances.enabled"
)

var (
	// Path to default tarantool.yaml configuration file.
	// Defined at build time, see magefile.
	defaultConfigPath string
)

// getDefaultCliOpts returns `CliOpts`filled with default values.
func getDefaultCliOpts() *config.CliOpts {
	modules := config.ModulesOpts{
		Directory: "",
	}
	app := config.AppOpts{
		InstancesEnabled: ".",
		RunDir:           "run",
		LogDir:           "log",
		LogMaxSize:       0,
		LogMaxAge:        0,
		LogMaxBackups:    0,
		Restartable:      false,
		DataDir:          "data",
		BinDir:           "",
		IncludeDir:       "",
	}
	ee := config.EEOpts{
		CredPath: "",
	}
	repo := config.RepoOpts{
		Rocks:   "",
		Install: filepath.Join(defaultConfigPath, "distfiles"),
	}
	return &config.CliOpts{Modules: &modules, App: &app, Repo: &repo, EE: &ee}
}

// adjustPathWithConfigLocation adjust provided filePath with configDir.
// Absolute filePath is returned as is. Relative filePath is calculated relative to configDir.
// If filePath is empty, defaultDirName is appended to configDir.
func adjustPathWithConfigLocation(filePath string, configDir string, defaultDirName string) string {
	if filePath == "" {
		return filepath.Join(configDir, defaultDirName)
	}
	return util.GetAbsPath(configDir, filePath)
}

// GetCliOpts returns Tarantool CLI options from the config file
// located at path configurePath.
func GetCliOpts(configurePath string) (*config.CliOpts, error) {
	var cfg config.Config
	// Config could not be processed.
	if _, err := os.Stat(configurePath); err != nil {
		// TODO: Add warning in next patches, discussion
		// what if the file exists, but access is denied, etc.
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("Failed to get access to configuration file: %s", err)
		}

		cfg.CliConfig = getDefaultCliOpts()
		return cfg.CliConfig, nil
	}

	rawConfigOpts, err := util.ParseYAML(configurePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	if err := mapstructure.Decode(rawConfigOpts, &cfg); err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	if cfg.CliConfig == nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: missing tt section")
	}

	configDir := filepath.Dir(configurePath)
	if cfg.CliConfig.App == nil {
		cfg.CliConfig.App = &config.AppOpts{
			InstancesEnabled: ".",
			RunDir:           "run",
			LogDir:           "log",
			Restartable:      false,
			DataDir:          "data",
			BinDir:           filepath.Join(configDir, "bin"),
			IncludeDir:       filepath.Join(configDir, "include"),
		}
	}
	if cfg.CliConfig.Repo == nil {
		cfg.CliConfig.Repo = &config.RepoOpts{
			Rocks:   "",
			Install: filepath.Join(configDir, "distfiles"),
		}
	}
	if cfg.CliConfig.App.InstancesEnabled == "" {
		cfg.CliConfig.App.InstancesEnabled = configDir
	}
	cfg.CliConfig.App.RunDir = adjustPathWithConfigLocation(cfg.CliConfig.App.RunDir,
		configDir, "run")
	cfg.CliConfig.App.LogDir = adjustPathWithConfigLocation(cfg.CliConfig.App.LogDir,
		configDir, "log")
	cfg.CliConfig.App.DataDir = adjustPathWithConfigLocation(cfg.CliConfig.App.DataDir,
		configDir, "data")
	cfg.CliConfig.App.BinDir = adjustPathWithConfigLocation(cfg.CliConfig.App.BinDir,
		configDir, "bin")
	cfg.CliConfig.App.IncludeDir = adjustPathWithConfigLocation(cfg.CliConfig.App.IncludeDir,
		configDir, "include")
	cfg.CliConfig.Repo.Install = adjustPathWithConfigLocation(cfg.CliConfig.Repo.Install,
		configDir, "local")

	if cfg.CliConfig.Modules != nil {
		cfg.CliConfig.Modules.Directory = util.GetAbsPath(configDir,
			cfg.CliConfig.Modules.Directory)
	}

	for i := range cfg.CliConfig.Templates {
		cfg.CliConfig.Templates[i].Path = adjustPathWithConfigLocation(
			cfg.CliConfig.Templates[i].Path, configDir, ".")
	}

	return cfg.CliConfig, nil
}

// ValidateCliOpts checks for ambiguous config options.
func ValidateCliOpts(cliCtx *cmdcontext.CliCtx) error {
	if cliCtx.LocalLaunchDir != "" {
		if cliCtx.IsSystem {
			return fmt.Errorf("You can specify only one of -L(--local) and -S(--system) options")
		}
		if cliCtx.ConfigPath != "" {
			return fmt.Errorf("You can specify only one of -L(--local) and -с(--cfg) options")
		}
	} else {
		if cliCtx.IsSystem && cliCtx.ConfigPath != "" {
			return fmt.Errorf("You can specify only one of -S(--system) and -с(--cfg) options")
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
			return fmt.Errorf("Specified path to the configuration file is invalid: %s", err)
		}
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
		if module, ok := (*modulesInfo)[cmd.Name()]; ok {
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
	if module, ok := (*modulesInfo)[externalCmd]; ok {
		if !module.IsInternal {
			rootCmd.AddCommand(newExternalCommand(cmdCtx, modulesInfo, externalCmd, nil))
			helpCmd.AddCommand(newExternalCommand(cmdCtx, modulesInfo, externalCmd,
				[]string{"--help"}))
		}
	}
}

// newExternalCommand returns a pointer to a new external
// command that will call modules.RunCmd.
func newExternalCommand(cmdCtx *cmdcontext.CmdCtx, modulesInfo *modules.ModulesInfo,
	cmdName string, addArgs []string) *cobra.Command {
	cmd := &cobra.Command{
		Use: cmdName,
		Run: func(cmd *cobra.Command, args []string) {
			if addArgs != nil {
				args = append(args, addArgs...)
			}

			cmdCtx.Cli.ForceInternal = false
			if err := modules.RunCmd(cmdCtx, cmdName, modulesInfo, nil, args); err != nil {
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
			return fmt.Errorf(`Found Tarantool binary '%s' isn't executable: %s`,
				localTarantool, err)
		}

		cmdCtx.Cli.TarantoolExecutable = localTarantool
		cmdCtx.Cli.IsTarantoolBinFromRepo = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("Failed to get access to Tarantool binary file: %s", err)
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
		cmdCtx.Cli.ConfigPath = filepath.Join(launchDir, ConfigName)
		// TODO: Add warning messages, discussion what if the file
		// exists, but access is denied, etc.
		if _, err := os.Stat(cmdCtx.Cli.ConfigPath); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("Failed to get access to configuration file: %s", err)
			}

			var err error
			if cmdCtx.Cli.ConfigPath, err = getConfigPath(ConfigName); err != nil {
				return fmt.Errorf("Failed to get Tarantool CLI config: %s", err)
			}

			if cmdCtx.Cli.ConfigPath == "" {
				if cmdCtx.Cli.LocalLaunchDir != "" {
					return fmt.Errorf("Failed to find Tarantool CLI config for '%s'",
						cmdCtx.Cli.LocalLaunchDir)
				}
				cmdCtx.Cli.ConfigPath = getSystemConfigPath()
			}
		}
	}

	cliOpts, err := GetCliOpts(cmdCtx.Cli.ConfigPath)
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
					`Found tt binary in local directory "%s" isn't executable: %s`, launchDir, err)
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
			return fmt.Errorf("Failed to get access to tt binary file: %s", err)
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
			return fmt.Errorf(`Failed to get absolute path to local directory: %s`, err)
		}

		log.Debugf("Local launch directory: %s", launchDir)

		if _, err = util.Chdir(launchDir); err != nil {
			return fmt.Errorf(`Failed to change working directory: %s`, err)
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
			return fmt.Errorf("Failed to get Tarantool CLI config: %s", err)
		}
	}

	if cmdCtx.Cli.ConfigPath != "" {
		return configureLocalCli(cmdCtx)
	}

	return configureSystemCli(cmdCtx)
}

// getConfigPath looks for the path to the tarantool.yaml configuration file,
// looking through all directories from the current one to the root.
// This search pattern is chosen for the convenience of the user.
func getConfigPath(configName string) (string, error) {
	curDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("Failed to detect current directory: %s", err)
	}

	for curDir != "/" {
		configPath := filepath.Join(curDir, ConfigName)
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}

		curDir = filepath.Dir(curDir)
	}

	return "", nil
}
