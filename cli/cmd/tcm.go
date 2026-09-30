package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/tail"
	tcmCmd "github.com/tarantool/tt/v3/cli/tcm"
	libwatchdog "github.com/tarantool/tt/v3/lib/watchdog"
)

var (
	errCannotStartTCMBinaryIsNotFound = errors.New("cannot start: tcm binary is not found")
	errProcessIsNotRunning            = errors.New("process is not running")
)

var tcmCtx = tcmCmd.TcmCtx{
	Executable:      "",
	Watchdog:        false,
	WatchdogPidFile: "",
	Log: tcmCmd.LoggerOpts{
		Level:      "",
		Lines:      0,
		IsFollow:   false,
		NoColor:    false,
		ForceColor: false,
		NoFormat:   false,
	},
}

const (
	tcmPidFile             = "tcm.pid"
	watchdogPidFile        = "watchdog.pid"
	logFileName            = "tcm.log"
	tcmDefaultLogLines     = 10
	watchdogRestartDelay   = 5 * time.Second
	statusPIDColumn        = 2
	statusExecutableColumn = 3
	statusStateColumn      = 4
)

func newTcmStartCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   "start [flags]",
		Short: "Start tcm application",
		Long: `Start to the tcm.
		tt tcm start --watchdog
		tt tcm start --path`,
		RunE: internalRunE(internalStartTcm),
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
		RunE: internalRunE(internalTcmStatus),
	}

	return tcmCmd
}

func newTcmStopCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop tcm application",
		Long:  `Stop to the tcm. tt tcm stop`,
		RunE:  internalRunE(internalTcmStop),
	}

	return tcmCmd
}

func newTcmLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log [flags]",
		Short: "Show tcm application logs",
		Long:  `Show logs for the tcm. tt tcm log`,
		RunE:  internalRunE(internalTcmLog),
	}

	cmd.Flags().IntVarP(&tcmCtx.Log.Lines, "lines", "n", tcmDefaultLogLines,
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
	tcmApp := exec.CommandContext(context.Background(), tcmCtx.Executable,
		"--log.default.add-source",
		"--log.default.output=file",
		"--log.default.format=json",
		"--log.default.level="+logLevel,
		"--log.default.file.name="+logFileName,
	)

	err := tcmApp.Start()
	if err != nil {
		return fmt.Errorf("failed to start tcm: %w", err)
	}

	if tcmApp == nil || tcmApp.Process == nil {
		return errProcessIsNotRunning
	}

	owned, err := process_utils.CreatePIDFile(tcmPidFile, tcmApp.Process.Pid)
	if err != nil {
		return err
	}

	// tt exits and TCM goes on: the file is left to the pid it names, which
	// keeps another start from taking it over while TCM runs.
	err = owned.Keep()
	if err != nil {
		return err
	}

	log.Infof("Interactive process PID %d written to %q\n", tcmApp.Process.Pid, tcmPidFile)

	return nil
}

func startTcmUnderWatchDog() error {
	wd := libwatchdog.NewWatchdog(tcmPidFile, watchdogPidFile, watchdogRestartDelay)

	return wd.Start(tcmCtx.Executable)
}

func internalStartTcm(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if cmdCtx.Cli.TarantoolCli.Executable == "" {
		return fmt.Errorf("cannot start: %w", errTarantoolBinaryNotFound)
	}

	if cmdCtx.Cli.TcmCli.Executable == "" {
		return errCannotStartTCMBinaryIsNotFound
	}

	tcmCtx.Executable = cmdCtx.Cli.TcmCli.Executable

	if !tcmCtx.Watchdog {
		return startTcmInteractive(tcmCtx.Log.Level)
	}

	return startTcmUnderWatchDog()
}

func internalTcmStatus(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	pidAbsPath, err := filepath.Abs(tcmPidFile)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of %q: %w", tcmPidFile, err)
	}

	_, err = os.Stat(pidAbsPath)
	if err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	}

	statusTable := table.NewWriter()
	statusTable.SetOutputMirror(os.Stdout)

	statusTable.AppendHeader(
		table.Row{"APPLICATION", "STATUS", "PID"})

	statusTable.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: statusPIDColumn, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: statusExecutableColumn, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: statusStateColumn, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
	})

	status := process_utils.ProcessStatus(pidAbsPath)

	statusTable.AppendRows([]table.Row{
		{"TCM", status.Status, status.PID},
	})
	statusTable.Render()

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

	printer := tcmCmd.NewLogPrinter(tcmCtx.Log.NoFormat, tcmCtx.Log.NoColor, os.Stdout)
	if tcmCtx.Log.IsFollow {
		f := tail.NewTailFollower(logFileName)
		return tcmCmd.FollowLogs(f, printer, tcmCtx.Log.Lines)
	}

	t := tail.NewTailReader(logFileName)

	return tcmCmd.TailLogs(t, printer, tcmCtx.Log.Lines)
}
