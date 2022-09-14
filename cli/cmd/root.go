package cmd

import (
	"fmt"
	"os"

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
		return fmt.Errorf("Can't inject commands. The root is nil.")
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
	}

	rootCmd.PersistentFlags().BoolVarP(&cmdCtx.Cli.IsSystem, "system", "S",
		false, "System launch")
	rootCmd.PersistentFlags().StringVarP(&cmdCtx.Cli.LocalLaunchDir, "local", "L",
		"", "Local launch")
	rootCmd.PersistentFlags().BoolVarP(&cmdCtx.Cli.ForceInternal, "internal", "I",
		false, "Use internal module")
	rootCmd.PersistentFlags().StringVarP(&cmdCtx.Cli.ConfigPath, "cfg", "c",
		"", "Path to configuration file")

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
		NewRemoveCmd(),
		NewPackCmd(),
	)
	if err := injectCmds(rootCmd); err != nil {
		panic(err.Error())
	}

	rootCmd.InitDefaultHelpCmd()

	log.SetHandler(cli.Default)

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

	// Configure Tarantool CLI.
	if err := configure.Cli(&cmdCtx); err != nil {
		log.Fatalf("Failed to configure Tarantool CLI: %s", err)
	}

	var err error
	cliOpts, err = configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		log.Fatalf("Failed to get Tarantool CLI configuration: %s", err)
	}

	// Setup TT_INST_AVAILABLE with instances_available path.
	// Required for cartridge.
	if cliOpts.App != nil {
		os.Setenv("TT_INST_AVAILABLE", cliOpts.App.InstancesAvailable)
	}

	// Getting modules information.
	modulesInfo, err = modules.GetModulesInfo(&cmdCtx, rootCmd.Commands(), cliOpts)
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
