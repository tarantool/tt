package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/uninstall"
)

var (
	programName string
)

// newUninstallTtCmd creates a command to install tt.
func newUninstallTtCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tt [version]",
		Short: "Uninstall tt",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			programName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				InternalUninstallModule, args)
			handleCmdErr(cmd, err)
		},
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return []string{}, cobra.ShellCompDirectiveNoFileComp
			}
			return uninstall.GetList(cliOpts, cmd.Name()),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	return tntCmd
}

// newUninstallTarantoolCmd creates a command to install tarantool.
func newUninstallTarantoolCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tarantool [version]",
		Short: "Uninstall tarantool community edition",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			programName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				InternalUninstallModule, args)
			handleCmdErr(cmd, err)
		},
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return []string{}, cobra.ShellCompDirectiveNoFileComp
			}
			return uninstall.GetList(cliOpts, cmd.Name()),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	return tntCmd
}

// newUninstallTarantoolEeCmd creates a command to install tarantool-ee.
func newUninstallTarantoolEeCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:   "tarantool-ee [version]",
		Short: "Uninstall tarantool enterprise edition",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			programName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				InternalUninstallModule, args)
			handleCmdErr(cmd, err)
		},
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return []string{}, cobra.ShellCompDirectiveNoFileComp
			}
			return uninstall.GetList(cliOpts, cmd.Name()),
				cobra.ShellCompDirectiveNoFileComp
		},
	}

	return tntCmd
}

// newUninstallTarantoolDevCmd creates a command to uninstall tarantool-dev.
func newUninstallTarantoolDevCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tarantool-dev",
		Short: "Uninstall tarantool-dev",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			programName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				InternalUninstallModule, args)
			handleCmdErr(cmd, err)
		},
	}

	return tntCmd
}

// NewUninstallCmd creates uninstall command.
func NewUninstallCmd() *cobra.Command {
	var uninstallCmd = &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstalls a program",
		Example: `
# To uninstall Tarantool:

    $ tt uninstall tarantool <version>`,
	}

	uninstallCmd.AddCommand(
		newUninstallTtCmd(),
		newUninstallTarantoolCmd(),
		newUninstallTarantoolEeCmd(),
		newUninstallTarantoolDevCmd(),
	)

	return uninstallCmd
}

// InternalUninstallModule is a default uninstall module.
func InternalUninstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	programVersion := ""
	if len(args) == 1 {
		programVersion = args[0]
	} else if len(args) > 1 {
		return fmt.Errorf("wrong number of arguments")
	}

	err := uninstall.UninstallProgram(programName, programVersion, cliOpts.Env.BinDir,
		cliOpts.Env.IncludeDir+"/include", cmdCtx)
	return err
}
