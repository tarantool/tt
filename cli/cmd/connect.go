package cmd

import (
	"fmt"
	"syscall"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	connectUser     string
	connectPassword string
	connectFile     string
)

// NewConnectCmd creates connect command.
func NewConnectCmd() *cobra.Command {
	var connectCmd = &cobra.Command{
		Use: "connect (<INSTANCE_NAME> | <URI>) [<FILE> | <COMMAND>] [flags]\n" +
			"  COMMAND | tt connect (<INSTANCE_NAME> | <URI>) [flags]",
		Short: "Connect to the tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalConnectModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	connectCmd.Flags().StringVarP(&connectUser, "username", "u", "", "username")
	connectCmd.Flags().StringVarP(&connectPassword, "password", "p", "", "password")
	connectCmd.Flags().StringVarP(&connectFile, "file", "f", "",
		`file to read the script for evaluation. "-" - read the script from stdin`)

	return connectCmd
}

// resolveInstAddr checks if the instance name is used as the address and
// replaces it with a control socket if so.
func resolveInstAddr(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts,
	args []string) ([]string, error) {
	var err error
	newArgs := args

	// FillCtx returns error if no instances found.
	if err = running.FillCtx(cliOpts, cmdCtx, args); err != nil {
		return newArgs, err
	}

	if len(cmdCtx.Running) > 1 {
		return newArgs, fmt.Errorf("specify instance name")
	}

	newArgs[0] = cmdCtx.Running[0].ConsoleSocket

	return newArgs, nil
}

// internalConnectModule is a default connect module.
func internalConnectModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	argsLen := len(args)
	if argsLen != 1 {
		return fmt.Errorf("Incorrect combination of command parameters")
	}

	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	cmdCtx.Connect.Username = connectUser
	cmdCtx.Connect.Password = connectPassword
	cmdCtx.Connect.SrcFile = connectFile

	newArgs, err := resolveInstAddr(cmdCtx, cliOpts, args)
	if err != nil {
		return err
	}

	if connectFile == "" {
		if terminal.IsTerminal(syscall.Stdin) {
			log.Info("Connecting to the instance...")
		}
		if err := connect.Connect(cmdCtx, newArgs); err != nil {
			return err
		}
	} else {
		res, err := connect.Eval(cmdCtx, newArgs)
		if err != nil {
			return err
		}
		// "Println" is used instead of "log..." to print the result without any decoration.
		fmt.Println(string(res))
	}

	return nil
}
