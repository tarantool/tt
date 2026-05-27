package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// errNoConfig is returned if environment config file tt.yaml not found.
var errNoConfig = errors.New(configure.ConfigName +
	" not found, you need to create tt environment config with 'tt init'" +
	" or provide exact config location with --cfg option")

// isConfigExist returns `true` if environment config file tt.yaml exist.
func isConfigExist(cmdCtx *cmdcontext.CmdCtx) bool {
	return cmdCtx.Cli.ConfigPath != ""
}

func RunModuleFunc(internalModule modules.InternalFunc) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		cmdCtx.CommandName = cmd.Name()
		err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo, internalModule, args)
		if err != nil {
			util.HandleCmdErr(cmd, err)
		}
	}
}
