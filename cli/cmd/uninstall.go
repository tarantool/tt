package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/uninstall"
)

func uninstallValidArgsFunc(cmd *cobra.Command, args []string, toComplete string) (
	[]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return []string{}, cobra.ShellCompDirectiveNoFileComp
	}
	return uninstall.GetList(cliOpts, cmd.Name()),
		cobra.ShellCompDirectiveNoFileComp
}

// newUninstallTtCmd creates a command to install tt.
func newUninstallTtCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:               "tt [version]",
		Short:             "Uninstall tt",
		Run:               TtModuleCmdRun(internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: uninstallValidArgsFunc,
	}

	return tntCmd
}

// newUninstallTarantoolCmd creates a command to install tarantool.
func newUninstallTarantoolCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:               "tarantool [version]",
		Short:             "Uninstall tarantool community edition",
		Run:               TtModuleCmdRun(internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: uninstallValidArgsFunc,
	}

	return tntCmd
}

// newUninstallTarantoolEeCmd creates a command to install tarantool-ee.
func newUninstallTarantoolEeCmd() *cobra.Command {
	var tntCmd = &cobra.Command{
		Use:               "tarantool-ee [version]",
		Short:             "Uninstall tarantool enterprise edition",
		Run:               TtModuleCmdRun(internalUninstallModule),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: uninstallValidArgsFunc,
	}

	return tntCmd
}

// newUninstallTarantoolDevCmd creates a command to uninstall tarantool-dev.
func newUninstallTarantoolDevCmd() *cobra.Command {
	tntCmd := &cobra.Command{
		Use:   "tarantool-dev",
		Short: "Uninstall tarantool-dev",
		Run:   TtModuleCmdRun(internalUninstallModule),
		Args:  cobra.ExactArgs(0),
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

// internalUninstallModule is a default uninstall module.
func internalUninstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	programName := cmdCtx.CommandName
	programVersion := ""
	if len(args) == 1 {
		programVersion = args[0]
	}

	err := uninstall.UninstallProgram(programName, programVersion, cliOpts.Env.BinDir,
		cliOpts.Env.IncludeDir+"/include")
	return err
}
