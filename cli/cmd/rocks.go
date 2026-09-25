package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/rocks"
)

// NewRocksCmd creates rocks command.
func NewRocksCmd() *cobra.Command {
	rocksCmd := &cobra.Command{
		Use:   "rocks",
		Short: "LuaRocks package manager",
		// Disabled all flags parsing on this commands leaf.
		// LuaRocks will handle it self.
		DisableFlagParsing: true,
		RunE:               RunModuleFuncE(internalRocksModule),
	}

	return rocksCmd
}

// internalRocksModule is a default rocks module.
func internalRocksModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	return rocks.Exec(cmdCtx, cliOpts, args)
}
