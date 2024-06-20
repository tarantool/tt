package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/enable"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// NewEnableCmd creates a new enable command.
func NewEnableCmd() *cobra.Command {
	var initCmd = &cobra.Command{
		Use: "enable <APP_PATH> | <SCRIPT_PATH>",
		Short: "Create a symbolic link in 'instances_enabled' directory " +
			"to a script or an application directory",
		Example: `
# Create a symbolic link in 'instances_enabled' directory to a script.
	$ tt enable Users/myuser/my_scripts/script.lua
# Create a symbolic link in 'instances_enabled' directory to an application directory.
	$ tt enable ../myuser/my_cool_app`,
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalEnableModule, args)
			util.HandleCmdErr(cmd, err)
		},
	}

	return initCmd
}

// internalEnableModule is a default enable module.
func internalEnableModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("provide the path to a script or application directory")
	}

	if cliOpts.Env.InstancesEnabled == "." {
		return fmt.Errorf("enabling application for instances enabled '.' is not supported")
	}

	return enable.Enable(args[0], cliOpts)
}
