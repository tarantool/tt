package cmd

import (
	"os"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/context"
	"github.com/tarantool/tt/cli/modules"
)

var (
	ctx         context.Ctx
	cliOpts     *modules.CliOpts
	modulesInfo modules.ModulesInfo
	rootCmd     *cobra.Command
)

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
	}

	rootCmd.PersistentFlags().BoolVarP(&ctx.Cli.IsSystem, "system", "S", false, "System launch")
	rootCmd.PersistentFlags().StringVarP(&ctx.Cli.LocalLaunchDir, "local", "L", "", "Local launch")
	rootCmd.PersistentFlags().BoolVarP(&ctx.Cli.ForceInternal, "internal", "I", false, "Use internal module")
	rootCmd.PersistentFlags().StringVarP(&ctx.Cli.ConfigPath, "cfg", "c", "", "Path to configuration file")

	log.SetHandler(cli.Default)

	return rootCmd
}

// Execute root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf(err.Error())
	}
}

// init initializes global flags, configures CLI, configure
// external modules, collects information about available
// modules and configure `help` module.
func init() {
	rootCmd = NewCmdRoot()
	rootCmd.ParseFlags(os.Args)

	// Configure Tarantool CLI.
	if err := configure.Cli(&ctx); err != nil {
		log.Fatalf("Failed to configure Tarantool CLI: %s", err)
	}

	var err error
	cliOpts, err = modules.GetCliOpts(ctx.Cli.ConfigPath)
	if err != nil {
		log.Fatalf("Failed to get Tarantool CLI configuration: %s", err)
	}

	// Getting modules information.
	modulesInfo, err = modules.GetModulesInfo(&ctx, rootCmd.Commands(), cliOpts)
	if err != nil {
		log.Fatalf("Failed to configure Tarantool CLI command: %s", err)
	}

	// External commands must be configured in a special way.
	// This is necessary, for example, so that we can pass arguments to these commands.
	if len(os.Args) > 1 {
		configure.ExternalCmd(rootCmd, &ctx, &modulesInfo, os.Args[1:])
	}
}
