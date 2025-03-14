package cmd

import (
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	tcmCmd "github.com/tarantool/tt/cli/tcm"
	"github.com/tarantool/tt/cli/util"
)

var tcmCtx = tcmCmd.TcmCtx{}

func newTcmStartCmd() *cobra.Command {
	var tcmCmd = &cobra.Command{
		Use:   "start",
		Short: "Start tcm application",
		Long: `Start to the tcm.
		tt tcm start --watchdog
		tt tcm start --path`,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo, internalStartTcm, args)
			util.HandleCmdErr(cmd, err)

		},
	}
	tcmCmd.Flags().StringVar(&tcmCtx.Executable, "path", "", "the path to the tcm binary file")
	tcmCmd.Flags().BoolVar(&tcmCtx.Watchdog, "watchdog", false, "enables the watchdog")

	return tcmCmd
}

func NewTcmCmd() *cobra.Command {
	var tcmCmd = &cobra.Command{
		Use:   "tcm",
		Short: "Manage tcm application",
	}
	tcmCmd.AddCommand(
		newTcmStartCmd(),
	)
	return tcmCmd
}

func startTcmInteractive() error {
	tcmApp := exec.Command(tcmCtx.Executable)

	tcmApp.Stdout = os.Stdout
	tcmApp.Stderr = os.Stderr

	if err := tcmApp.Run(); err != nil {
		return err
	}

	return nil
}

func startTcmUnderWatchDog() error {
	wd, err := tcmCmd.NewWatchdog(5 * time.Second)
	if err != nil {
		return err
	}

	if err := wd.Start(tcmCtx.Executable); err != nil {
		return err
	}

	return nil
}

func internalStartTcm(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if cmdCtx.Cli.TarantoolCli.Executable == "" {
		return errors.New("cannot start: tarantool binary is not found")
	}

	if cmdCtx.Cli.TcmCli.Executable == "" {
		return errors.New("cannot start: tcm binary is not found")
	}

	tcmCtx.Executable = cmdCtx.Cli.TcmCli.Executable

	if !tcmCtx.Watchdog {
		if err := startTcmInteractive(); err != nil {
			return err
		}
	}

	if err := startTcmUnderWatchDog(); err != nil {
		return err
	}

	return nil
}
