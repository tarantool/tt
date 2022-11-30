package cmd

import (
	"fmt"
	"strings"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/uninstall"
)

// NewUninstallCmd creates uninstall command.
func NewUninstallCmd() *cobra.Command {
	var uninstallCmd = &cobra.Command{
		Use:   "uninstall <PROGRAM>",
		Short: "Uninstalls a program",
		Long: "Uninstalls a program\n\n" +
			"Available programs:\n" +
			"tt - Tarantool CLI\n" +
			"tarantool - Tarantool\n" +
			"tarantool-ee - Tarantool enterprise edition",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				InternalUninstallModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return uninstall.GetList(&cmdCtx), cobra.ShellCompDirectiveNoFileComp
		},
		Example: `
# To uninstall Tarantool:

    $ tt uninstall tarantool=<version>`,
	}
	return uninstallCmd
}

// InternalUninstallModule is a default uninstall module.
func InternalUninstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}
	if !strings.Contains(args[0], search.VersionCliSeparator) {
		return fmt.Errorf("incorrect usage.\n   e.g program%sversion", search.VersionCliSeparator)
	}
	err = uninstall.UninstallProgram(args[0], cliOpts.App.BinDir,
		cliOpts.App.IncludeDir+"/include", cmdCtx)
	return err
}
