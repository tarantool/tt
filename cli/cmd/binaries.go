package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/binary"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/search"
	"golang.org/x/exp/slices"
)

// NewBinariesCmd creates binaries command.
func NewBinariesCmd() *cobra.Command {
	var binariesCmd = &cobra.Command{
		Use: "binaries",
	}

	var switchCmd = &cobra.Command{
		Use:   "switch [program] [version]",
		Short: "Switch to installed binary",
		Example: `
# Switch without any arguments.

	$ tt binaries switch

You will need to choose program and version using arrow keys in your console.

# Switch without version.

	$ tt binaries switch tarantool

You will need to choose version using arrow keys in your console.

# Switch with program and version.

	$ tt binaries switch tarantool 2.10.4`,
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalSwitchModule, args)
			handleCmdErr(cmd, err)
		},
	}
	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "Show a list of installed binaries and their versions.",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalListModule, args)
			handleCmdErr(cmd, err)
		},
	}
	binariesCmd.AddCommand(switchCmd)
	binariesCmd.AddCommand(listCmd)
	return binariesCmd
}

// internalSwitchModule is a switch module.
func internalSwitchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}
	var switchCtx binary.SwitchCtx
	supportedPrograms := []string{search.ProgramCe, search.ProgramEe, search.ProgramTt}

	var err error
	switch len(args) {
	case 2:
		switchCtx.Version = args[1]
		switchCtx.ProgramName = args[0]
		if !slices.Contains(supportedPrograms, switchCtx.ProgramName) {
			return fmt.Errorf("not supported program: %s", switchCtx.ProgramName)
		}
	case 1:
		switchCtx.ProgramName = args[0]
		if !slices.Contains(supportedPrograms, switchCtx.ProgramName) {
			return fmt.Errorf("not supported program: %s", switchCtx.ProgramName)
		}
		switchCtx.Version, err = binary.ChooseVersion(cliOpts.Env.BinDir, switchCtx.ProgramName)
		if err != nil {
			return err
		}
	case 0:
		switchCtx.ProgramName, err = binary.ChooseProgram(supportedPrograms)
		if err != nil {
			return err
		}
		switchCtx.Version, err = binary.ChooseVersion(cliOpts.Env.BinDir, switchCtx.ProgramName)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid number of arguments")

	}
	switchCtx.BinDir = cliOpts.Env.BinDir
	switchCtx.IncDir = cliOpts.Env.IncludeDir

	err = binary.Switch(switchCtx)
	return err
}

// internalListModule is a list module.
func internalListModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	return binary.ListBinaries(cmdCtx, cliOpts)
}
