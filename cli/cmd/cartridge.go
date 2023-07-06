package cmd

import (
	"github.com/spf13/cobra"
	cc "github.com/tarantool/cartridge-cli/cli/commands"
)

// NewCartridgeCmd chains commands from cartridge-cli to our cobra tree.
func NewCartridgeCmd() *cobra.Command {
	var cartridgeCmd = &cobra.Command{
		Use:   "cartridge",
		Short: "Manage cartridge application",
	}

	cartCliCmds := []*cobra.Command{
		cc.CartridgeCliAdmin,
		cc.CartridgeCliBench,
		cc.CartridgeCliFailover,
		cc.CartridgeCliRepair,
		cc.CartridgeCliReplica,
	}

	for _, cmd := range cartCliCmds {
		cartridgeCmd.AddCommand(cmd)
	}

	return cartridgeCmd
}
