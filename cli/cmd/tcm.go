package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/apex/log"
	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/tail"
	tcmCmd "github.com/tarantool/tt/cli/tcm"
	libwatchdog "github.com/tarantool/tt/lib/watchdog"
)

var tcmCtx = tcmCmd.TcmCtx{}

const (
	tcmPidFile      = "tcm.pid"
	watchdogPidFile = "watchdog.pid"
	logFileName     = "tcm.log"
)

func newTcmStartCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   "start [flags]",
		Short: "Start tcm application",
		Long: `Start to the tcm.
		tt tcm start --watchdog
		tt tcm start --path`,
		Run: RunModuleFunc(internalStartTcm),
	}
	tcmCmd.Flags().StringVar(&tcmCtx.Executable, "path", "", "the path to the tcm binary file")
	tcmCmd.Flags().BoolVar(&tcmCtx.Watchdog, "watchdog", false, "enables the watchdog")
	tcmCmd.Flags().StringVar(&tcmCtx.Log.Level, "log-level", "INFO",
		"log level for the tcm application")

	return tcmCmd
}

func newTcmStatusCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   "status",
		Short: "Status tcm application",
		Long: `Status to the tcm.
		tt tcm status`,
		Run: RunModuleFunc(internalTcmStatus),
	}
	return tcmCmd
}

func newTcmStopCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop tcm application",
		Long:  `Stop to the tcm. tt tcm stop`,
		Run:   RunModuleFunc(internalTcmStop),
	}
	return tcmCmd
}

func newTcmLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log [flags]",
		Short: "Show tcm application logs",
		Long:  `Show logs for the tcm. tt tcm log`,
		Run:   RunModuleFunc(internalTcmLog),
	}

	cmd.Flags().IntVarP(&tcmCtx.Log.Lines, "lines", "n", 10,
		"Count of last lines to output")
	cmd.Flags().BoolVarP(&tcmCtx.Log.IsFollow, "follow", "f", false,
		"Output appended data as the log file grows")
	cmd.Flags().BoolVar(&tcmCtx.Log.ForceColor, "color", false,
		"Force colored output in logs")
	cmd.Flags().BoolVar(&tcmCtx.Log.NoColor, "no-color", false,
		"Disable colored output in logs")
	cmd.Flags().BoolVar(&tcmCtx.Log.NoFormat, "no-format", false,
		"Disable log formatting")

	cmd.MarkFlagsMutuallyExclusive("color", "no-color")

	return cmd
}

func NewTcmCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   "tcm",
		Short: "Manage tcm application",
	}
	tcmCmd.AddCommand(
		newTcmStartCmd(),
		newTcmStatusCmd(),
		newTcmStopCmd(),
		newTcmLogCmd(),
	)
	return tcmCmd
}

func startTcmInteractive(logLevel string) error {
	tcmApp := exec.Command(tcmCtx.Executable,
		"--log.default.add-source",
		"--log.default.output=file",
		"--log.default.format=json",
		"--log.default.level="+logLevel,
		"--log.default.file.name="+logFileName,
	)

	if err := tcmApp.Start(); err != nil {
		return err
	}

	if tcmApp == nil || tcmApp.Process == nil {
		return errors.New("process is not running")
	}

	err := process_utils.CreatePIDFile(tcmPidFile, tcmApp.Process.Pid)
	if err != nil {
		return err
	}

	log.Infof("Interactive process PID %d written to %q\n", tcmApp.Process.Pid, tcmPidFile)
	return nil
}

func startTcmUnderWatchDog() error {
	wd := libwatchdog.NewWatchdog(tcmPidFile, watchdogPidFile, 5*time.Second)
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
		if err := startTcmInteractive(tcmCtx.Log.Level); err != nil {
			return err
		}
	} else {
		if err := startTcmUnderWatchDog(); err != nil {
			return err
		}
	}

	return nil
}

func internalTcmStatus(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	pidAbsPath, err := filepath.Abs(tcmPidFile)
	if err != nil {
		return err
	}

	if _, err := os.Stat(pidAbsPath); err != nil {
		return fmt.Errorf("path does not exist: %v", err)
	}

	ts := table.NewWriter()
	ts.SetOutputMirror(os.Stdout)

	ts.AppendHeader(
		table.Row{"APPLICATION", "STATUS", "PID"})

	ts.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 2, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 3, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 4, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
	})

	status := process_utils.ProcessStatus(pidAbsPath)

	ts.AppendRows([]table.Row{
		{"TCM", status.Status, status.PID},
	})
	ts.Render()
	return nil
}

func internalTcmStop(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if isExists, _ := process_utils.ExistsAndRecord(watchdogPidFile); isExists {
		_, err := process_utils.StopProcess(watchdogPidFile)
		if err != nil {
			return err
		}

		log.Info("Watchdog and TCM stopped")
	} else {
		_, err := process_utils.StopProcess(tcmPidFile)
		if err != nil {
			return err
		}

		log.Info("TCM stopped")
	}

	return nil
}

func internalTcmLog(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if tcmCtx.Log.ForceColor {
		color.NoColor = false
	}

	p := tcmCmd.NewLogPrinter(tcmCtx.Log.NoFormat, tcmCtx.Log.NoColor, os.Stdout)
	if tcmCtx.Log.IsFollow {
		f := tail.NewTailFollower(logFileName)
		return tcmCmd.FollowLogs(f, p, tcmCtx.Log.Lines)
	}

	t := tail.NewTailReader(logFileName)

	return tcmCmd.TailLogs(t, p, tcmCtx.Log.Lines)
}
