package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/process_utils"
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

func newTcmStatusCmd() *cobra.Command {
	var tcmCmd = &cobra.Command{
		Use:   "status",
		Short: "Status tcm application",
		Long: `Status to the tcm.
		tt tcm status`,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo, internalTcmStatus, args)
			util.HandleCmdErr(cmd, err)

		},
	}

	return tcmCmd
}

func NewTcmCmd() *cobra.Command {
	var tcmCmd = &cobra.Command{
		Use:   "tcm",
		Short: "Manage tcm application",
	}
	tcmCmd.AddCommand(
		newTcmStartCmd(),
		newTcmStatusCmd(),
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

	tcmCtx.PidFile = wd.PidFile
	fmt.Println("tcmCtx.PidFile", tcmCtx.PidFile)

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

func internalTcmStatus(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	// TODO проверить есть ли tcm?
	fmt.Println("run tcm status command")
	ts := table.NewWriter()
	ts.SetOutputMirror(os.Stdout)
	ts.AppendHeader(
		table.Row{"INSTANCE", "STATUS", "PID"})

	ts.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 2, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 3, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 4, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
	})

	status := process_utils.ProcessStatus(tcmCtx.PidFile)

	fmt.Println("tcmCtx.PidFile cmd status", tcmCtx.PidFile)
	fmt.Println("tcmCtx cmd status", tcmCtx)

	fmt.Println("status.Status", status.Status)
	fmt.Println("status.Pid", status.PID)

	ts.AppendRows([]table.Row{
		{"tcm", status.Status, status.PID},
	})
	ts.Render()
	return nil

}
