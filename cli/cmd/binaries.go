package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/binary"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/search"
	"golang.org/x/exp/slices"
)

var binariesSupportedPrograms = []string{
	search.ProgramCe,
	search.ProgramEe,
	search.ProgramTt,
}

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
		Run:  RunModuleFunc(internalSwitchModule),
		Args: cobra.MatchAll(cobra.MaximumNArgs(2), binariesSwitchValidateArgs),
	}
	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "Show a list of installed binaries and their versions.",
		Run:   RunModuleFunc(internalListModule),
	}
	binariesCmd.AddCommand(switchCmd)
	binariesCmd.AddCommand(listCmd)
	return binariesCmd
}

// binariesSwitchValidateArgs validates non-flag arguments of 'binaries switch' command.
func binariesSwitchValidateArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		if !slices.Contains(binariesSupportedPrograms, args[0]) {
			return fmt.Errorf("not supported program: %s", args[0])
		}
	}
	return nil
}

// internalSwitchModule is a switch module.
func internalSwitchModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}
	var switchCtx binary.SwitchCtx
	var err error

	if len(args) > 0 {
		switchCtx.ProgramName = args[0]
	} else {
		switchCtx.ProgramName, err = binary.ChooseProgram(binariesSupportedPrograms)
		if err != nil {
			return err
		}
	}

	if len(args) > 1 {
		switchCtx.Version = args[1]
	} else {
		switchCtx.Version, err = binary.ChooseVersion(cliOpts.Env.BinDir, switchCtx.ProgramName)
		if err != nil {
			return err
		}
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
