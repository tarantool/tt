package cmd

import (
	"syscall"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/modules"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	connectUser     string
	connectPassword string
)

// NewConnectCmd creates connect command.
func NewConnectCmd() *cobra.Command {
	var connectCmd = &cobra.Command{
		Use:   "connect [INSTANCE_NAME]",
		Short: "Connect to the tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalConnectModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	connectCmd.Flags().StringVarP(&connectUser, "username", "u", "", "username")
	connectCmd.Flags().StringVarP(&connectPassword, "password", "p", "", "password")

	return connectCmd
}

// internalConnectModule is a default connect module.
func internalConnectModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	// Fill CmdCtx.
	cmdCtx.Connect.Username = connectUser
	cmdCtx.Connect.Password = connectPassword

	if terminal.IsTerminal(syscall.Stdin) {
		log.Info("Connecting to the instance...")
	}
	if err := connect.Connect(cmdCtx, args); err != nil {
		return err
	}

	return nil
}
