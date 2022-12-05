package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/rocks"
)

// NewRocksCmd creates rocks command.
func NewRocksCmd() *cobra.Command {
	var rocksCmd = &cobra.Command{
		Use:   "rocks",
		Short: "LuaRocks package manager",
		// Disabled all flags parsing on this commands leaf.
		// LuaRocks will handle it self.
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalRocksModule, args)
			handleCmdErr(cmd, err)
		},
	}

	return rocksCmd
}

// internalRocksModule is a default rocks module.
func internalRocksModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	return rocks.Exec(cmdCtx, args)
}
