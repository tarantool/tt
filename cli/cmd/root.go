package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/util"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/logging"
	"github.com/tarantool/tt/v3/cli/modules"
)

var (
	errNilCommandRoot = errors.New("can't inject commands. The root is nil")
	errUnknownCommand = errors.New("unknown command ")
)

var (
	cmdCtx      cmdcontext.CmdCtx
	cliOpts     *config.CliOpts
	modulesInfo modules.ModulesInfo
	rootCmd     *cobra.Command
	// logFormat is the value of the --log-format flag.
	logFormat = logging.FormatText

	// InjectedCmds is populated with the command to be injected into root.
	// TT-EE.
	InjectedCmds []*cobra.Command
)

// GetCmdCtxPtr returns a pointer to cmdCtx, which can be used to create injected commands.
// TT-EE.
func GetCmdCtxPtr() *cmdcontext.CmdCtx {
	return &cmdCtx
}

// injectCmds injects additional commands.
// TT-EE.
func injectCmds(root *cobra.Command) error {
	if root == nil {
		return errNilCommandRoot
	}

	if InjectedCmds == nil {
		return nil
	}

	for i := range InjectedCmds {
		cmd := InjectedCmds[i]

		// Injected command must override the original one.
		// So, remove the original from the root.
		origCmds := root.Commands()
		for j := range origCmds {
			if cmd.Name() == origCmds[j].Name() {
				root.RemoveCommand(origCmds[j])
				break
			}
		}

		root.AddCommand(cmd)
	}

	return nil
}

// GetModulesInfoPtr returns a pointer to modulesInfo, which can be used to create
// injected commands.
// TT-EE.
func GetModulesInfoPtr() *modules.ModulesInfo {
	return &modulesInfo
}

// errorLogWriter logs what cobra writes to its error stream - "Error: unknown
// flag: --foo", "Run 'tt --help' for usage." - as error records, so that it
// comes out in the configured log format like any other error.
type errorLogWriter struct{}

// Write logs p, without its trailing newline, as one error record.
func (errorLogWriter) Write(p []byte) (int, error) {
	log.Error(strings.TrimRight(string(p), "\n"))

	return len(p), nil
}

// NewCmdRoot creates a new root command.
func NewCmdRoot() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tt",
		Short: "Tarantool CLI",
		Long:  "Utility for managing Tarantool packages and Tarantool-based applications",
		Example: `$ tt -L /path/to/local/dir version
  $ tt -S -I help
  $ tt completion`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			} else {
				return fmt.Errorf("%w%s", errUnknownCommand, args[0])
			}
		},
		ValidArgsFunction: RootShellCompletionCommands,
		TraverseChildren:  true,
	}

	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.IsSystem, "system", "S",
		false, "System launch")
	rootCmd.Flags().StringVarP(&cmdCtx.Cli.LocalLaunchDir, "local", "L",
		"", "Local launch")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.ForceInternal, "internal", "I",
		false, "Use internal module")
	rootCmd.Flags().StringVarP(&cmdCtx.Cli.ConfigPath, "cfg", "c",
		"", "Path to configuration file")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.Verbose, "verbose", "V",
		false, "Verbose output")
	rootCmd.Flags().Var(&logFormat, "log-format",
		"Format of the log written to stderr: text or json")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.IsSelfExec, "self", "s",
		false, "Skip searching for other tt versions to run")
	rootCmd.Flags().BoolVarP(&cmdCtx.Cli.NoPrompt, "no-prompt", "",
		false, "Skip cli interaction using default behavior")

	integrity.RegisterIntegrityCheckFlag(rootCmd.Flags(), &cmdCtx.Cli.IntegrityCheck)
	integrity.RegisterIntegrityCheckPeriodFlag(rootCmd.Flags(), &cmdCtx.Cli.IntegrityCheckPeriod)

	rootCmd.Flags().SetInterspersed(false)

	rootCmd.AddCommand(
		NewVersionCmd(),
		NewCompletionCmd(),
		NewStartCmd(),
		NewStopCmd(),
		NewStatusCmd(),
		NewRestartCmd(),
		NewLogrotateCmd(),
		NewCheckCmd(),
		NewConnectCmd(),
		NewRocksCmd(),
		NewCatCmd(),
		NewPlayCmd(),
		NewClusterCmd(),
		NewCoredumpCmd(),
		NewReplicasetCmd(),
		NewRunCmd(),
		NewTestCmd(),
		NewSearchCmd(),
		NewCleanCmd(),
		NewCreateCmd(),
		NewNewCmd(),
		NewInstallCmd(),
		NewUninstallCmd(),
		NewPackageCmd(),
		NewRegistryCmd(),
		NewErrorsHelpTopic(),
		NewDaemonCmd(),
		NewCfgCmd(),
		NewBinariesCmd(),
		NewEnvCmd(),
		NewDownloadCmd(),
		NewKillCmd(),
		NewLogCmd(),
		NewAeonCmd(),
		NewTcmCmd(),
		NewModulesCmd(),
	)
	if err := injectCmds(rootCmd); err != nil {
		panic(err.Error())
	}

	rootCmd.SetErr(errorLogWriter{})

	return rootCmd
}

// Execute root command.
// If received error is of an ArgError type, usage help is printed.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var argError *util.ArgError
		if errors.As(err, &argError) {
			log.Error(argError.Error())
			_ = rootCmd.Usage()
		}
		os.Exit(1)
	}
}

// InitRoot initializes global flags, configures CLI, configure
// external modules, collects information about available
// modules and configure `help` module.
func InitRoot() {
	if err := initRoot(); err != nil {
		logging.Fatalf("%s", err)
	}
}

// setupLogging installs the process logger the root flags ask for.
func setupLogging() error {
	level := slog.LevelInfo
	if cmdCtx.Cli.Verbose {
		level = slog.LevelDebug
	}

	return logging.Setup(logging.Options{
		Level:      level,
		Format:     logFormat,
		Writer:     os.Stderr,
		Redactor:   log.Secrets(),
		IsTerminal: nil,
		LookupEnv:  os.LookupEnv,
	})
}

// initRoot does the work of InitRoot and returns the first failure.
func initRoot() error {
	rootCmd = NewCmdRoot()
	// A flag error is reported by rootCmd.Execute, once the logger is set up;
	// an invalid --log-format leaves the default in place until then.
	_ = rootCmd.ParseFlags(os.Args[1:])

	if err := setupLogging(); err != nil {
		return err
	}

	var err error

	_, configPathEnvSet := os.LookupEnv("TT_CLI_CFG")
	if cmdCtx.Cli.ConfigPath == "" && configPathEnvSet {
		configPathEnv, err := filepath.Abs(os.Getenv("TT_CLI_CFG"))
		if err != nil {
			return fmt.Errorf("failed getting config path from environment variable: %w", err)
		}
		cmdCtx.Cli.ConfigPath = configPathEnv
	}

	if err := configure.ValidateCliOpts(&cmdCtx.Cli); err != nil {
		return err
	}

	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("can't get current dir: %w", err)
	}

	configPath, _ := util.GetYamlFileName(
		filepath.Join(currentDir, configure.ConfigName),
		false,
	)

	// Initialize integrity before loading tt.yaml because the configuration loader
	// uses the integrity repository.
	cmdCtx.Integrity, err = integrity.InitializeIntegrityCheck(
		cmdCtx.Cli.IntegrityCheck,
		filepath.Dir(configPath),
	)
	if err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

	if err := configure.Cli(&cmdCtx); err != nil {
		return fmt.Errorf("Failed to configure Tarantool CLI: %w", err)
	}

	cliOpts, cmdCtx.Cli.ConfigPath, err = configure.GetCliOpts(cmdCtx.Cli.ConfigPath,
		cmdCtx.Integrity.Repository)
	if err != nil {
		return fmt.Errorf("Failed to get Tarantool CLI configuration: %w", err)
	}
	if cmdCtx.Cli.ConfigPath == "" {
		// Config is not found, use current dir as base dir.
		if cmdCtx.Cli.ConfigDir, err = os.Getwd(); err != nil {
			return err
		}
	} else {
		cmdCtx.Cli.ConfigDir = filepath.Dir(cmdCtx.Cli.ConfigPath)
		log.Debugf("Using configuration file %q", cmdCtx.Cli.ConfigPath)
	}

	// TCM config, if any, is located next to tt config.
	tcmConfigBasename := filepath.Join(cmdCtx.Cli.ConfigDir, "tcm")
	cmdCtx.Cli.TcmCli.ConfigPath, _ = util.GetYamlFileName(tcmConfigBasename, false)

	// Getting modules information.
	modulesInfo, err = modules.GetModulesInfo(&cmdCtx, rootCmd.Name(), cliOpts)
	if err != nil {
		return fmt.Errorf("Failed to configure Tarantool CLI command: %w", err)
	}

	// External commands must be configured in a special way.
	// This is necessary, for example, so that we can pass arguments to these commands.
	configureExternalCmd(rootCmd, &modulesInfo, cmdCtx.Cli.ForceInternal)

	// Configure help command.
	configureHelpCommand(rootCmd, &modulesInfo)

	return nil
}
