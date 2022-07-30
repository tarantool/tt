package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/remove"
)

// NewRemoveCmd creates remove command.
func NewRemoveCmd() *cobra.Command {
	var removeCmd = &cobra.Command{
		Use:   "remove [OPTIONS] what",
		Short: "remove tarantool/tt",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, InternalRemoveModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return removeCmd
}

// InternalRemoveModule is a default remove module.
func InternalRemoveModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	err = remove.Remove(args[0], cliOpts.App.BinDir)
	return err
}
