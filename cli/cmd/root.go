package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
)

var (
	cmdCtx      cmdcontext.CmdCtx
	cliOpts     *config.CliOpts
	modulesInfo modules.ModulesInfo
	rootCmd     *cobra.Command

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
		return fmt.Errorf("can't inject commands. The root is nil")
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

// LogHandler is custom log handler implementation. It is used to send log entries to
// different streams: stdout and stderr. This is a decorator for the cli.Handler.
type LogHandler struct {
	stdoutHandler log.Handler
	stderrHandler log.Handler
}

// HandleLog performs log handling in accordance with log entry level.
func (handler *LogHandler) HandleLog(logEntry *log.Entry) error {
	switch logEntry.Level {
	case log.ErrorLevel, log.WarnLevel, log.FatalLevel:
		return handler.stderrHandler.HandleLog(logEntry)
	default:
		return handler.stdoutHandler.HandleLog(logEntry)
	}
}

// NewLogHandler creates a new log handler.
func NewLogHandler() *LogHandler {
	return &LogHandler{
		cli.New(os.Stdout),
		cli.New(os.Stderr),
	}
}

var defaultLogHandler = NewLogHandler()

// NewCmdRoot creates a new root command.
func NewCmdRoot() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "tt",
		Short: "Tarantool CLI",
		Long:  "Utility for managing Tarantool packages and Tarantool-based applications",
		Example: `$ tt version -L /path/to/local/dir
  $ tt help -S -I
  $ tt completion`,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
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
		NewCartridgeCmd(),
		NewCoredumpCmd(),
		NewRunCmd(),
		NewSearchCmd(),
		NewCleanCmd(),
		NewCreateCmd(),
		NewBuildCmd(),
		NewInstallCmd(),
		NewUninstallCmd(),
		NewPackCmd(),
		NewInitCmd(),
		NewDaemonCmd(),
		NewCfgCmd(),
		NewInstancesCmd(),
		NewBinariesCmd(),
	)
	if err := injectCmds(rootCmd); err != nil {
		panic(err.Error())
	}

	rootCmd.InitDefaultHelpCmd()

	log.SetHandler(defaultLogHandler)

	return rootCmd
}

// Execute root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf(err.Error())
	}
}

// InitRoot initializes global flags, configures CLI, configure
// external modules, collects information about available
// modules and configure `help` module.
func InitRoot() {
	rootCmd = NewCmdRoot()
	rootCmd.ParseFlags(os.Args)

	if err := configure.ValidateCliOpts(&cmdCtx.Cli); err != nil {
		log.Fatal(err.Error())
	}

	// Configure Tarantool CLI.
	if err := configure.Cli(&cmdCtx); err != nil {
		log.Fatalf("Failed to configure Tarantool CLI: %s", err)
	}

	var err error
	cliOpts, cmdCtx.Cli.ConfigPath, err = configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		log.Fatalf("Failed to get Tarantool CLI configuration: %s", err)
	}
	if cmdCtx.Cli.ConfigPath == "" {
		// Config is not found, use current dir as base dir.
		if cmdCtx.Cli.ConfigDir, err = os.Getwd(); err != nil {
			log.Fatal(err.Error())
		}
	} else {
		cmdCtx.Cli.ConfigDir = filepath.Dir(cmdCtx.Cli.ConfigPath)
	}

	// Setup TT_INST_ENABLED with instances_enabled path.
	// Required for cartridge.
	if cliOpts.App != nil {
		os.Setenv("TT_INST_ENABLED", cliOpts.App.InstancesEnabled)
	}

	// Getting modules information.
	modulesInfo, err = modules.GetModulesInfo(&cmdCtx, rootCmd, cliOpts)
	if err != nil {
		log.Fatalf("Failed to configure Tarantool CLI command: %s", err)
	}

	// External commands must be configured in a special way.
	// This is necessary, for example, so that we can pass arguments to these commands.
	if len(os.Args) > 1 {
		configure.ExternalCmd(rootCmd, &cmdCtx, &modulesInfo, os.Args[1:])
	}

	// Configure help command.
	configureHelpCommand(&cmdCtx, rootCmd)
}
