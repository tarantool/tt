package cmd

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/modules"
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
		Use: "connect (<INSTANCE_NAME> | <URI>) [<FILE> | <COMMAND>]\n" +
			"  COMMAND | tt connect (<INSTANCE_NAME> | <URI>)",
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

	runDir := cliOpts.App.RunDir
	if runDir == "" {
		if runDir, err = os.Getwd(); err != nil {
			return newArgs, err
		}
	}

	files, err := ioutil.ReadDir(runDir)
	if err != nil {
		return newArgs, err
	}

	for _, file := range files {
		if file.Name() == newArgs[0]+".pid" {
			newArgs[0] = filepath.Join(runDir, newArgs[0]+".control")
			break
		}
	}

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

	newArgs, _ := resolveInstAddr(cmdCtx, cliOpts, args)

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
