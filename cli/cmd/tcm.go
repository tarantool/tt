package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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
)

var (
	errTarantoolBinaryNotFound        = errors.New("tarantool binary is not found")
	errCannotStartTCMBinaryIsNotFound = errors.New("cannot start: tcm binary is not found")
	errProcessIsNotRunning            = errors.New("process is not running")
)

var tcmCtx = tcmCmd.TcmCtx{
	Executable: "",
	Watchdog:   false,
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
	watchdogStopTimeout    = 30 * time.Second
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

	// The pid file is taken before TCM starts, and TCM inherits its lock:
	// it owns the file for its whole life, as a watchdog owns its own, and
	// tt tcm status and stop tell whether it runs by the lock, not by the
	// pid, which may name another process once TCM is gone. A pid file a
	// running TCM owns keeps a second one from starting.
	owned, err := process_utils.ReservePIDFile(tcmPidFile)
	if err != nil {
		return err
	}

	tcmApp.ExtraFiles = []*os.File{owned.Lock()}

	err = tcmApp.Start()
	if err == nil && tcmApp.Process == nil {
		err = errProcessIsNotRunning
	}

	if err == nil {
		err = owned.SetPid(tcmApp.Process.Pid)
		if err != nil {
			_ = tcmApp.Process.Kill()
			_ = tcmApp.Wait()
		}
	}

	if err != nil {
		return errors.Join(fmt.Errorf("failed to start tcm: %w", err), owned.Release())
	}

	// tt exits and TCM goes on, holding the lock of the file.
	err = owned.Keep()
	if err != nil {
		return err
	}

	log.Infof("Interactive process PID %d written to %q\n", tcmApp.Process.Pid, tcmPidFile)

	return nil
}

// tcmWatchdogOpts are the options of the watchdog of TCM: the pid files in
// the working directory, and the integrity check period that
// tt instance start would use.
func tcmWatchdogOpts(cmdCtx *cmdcontext.CmdCtx, executable string) tcmCmd.WatchdogOpts {
	repository := cmdCtx.Integrity.Repository

	return tcmCmd.WatchdogOpts{
		Executable:   executable,
		PidFile:      watchdogPidFile,
		ChildPidFile: tcmPidFile,
		RestartDelay: watchdogRestartDelay,
		StopTimeout:  watchdogStopTimeout,
		CheckPeriod:  time.Duration(integrityCheckPeriodOf(&cmdCtx.Cli)) * time.Second,
		Check: func(context.Context) error {
			return repository.ValidateAll()
		},
	}
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

	return tcmCmd.RunWatchdog(tcmWatchdogOpts(cmdCtx, tcmCtx.Executable))
}

// tcmStatus tells the state of TCM the way tt tcm stop finds it: through a
// running watchdog first, which runs TCM or waits to restart it, and through
// the pid file of TCM started without a watchdog otherwise. The PID is the
// one of TCM, or of the watchdog while it has no TCM running.
//
// Both are told by the lock of the pid file (process_utils.OwnerStatus): the
// watchdog holds its own for its whole life and that of TCM while TCM runs,
// and TCM started without a watchdog holds its own, which it inherits from
// tt tcm start (startTcmInteractive).
func tcmStatus() process_utils.ProcessState {
	watchdog := process_utils.OwnerStatus(watchdogPidFile)
	if watchdog.Code != process_utils.ProcessRunningCode {
		return process_utils.OwnerStatus(tcmPidFile)
	}

	tcm := process_utils.OwnerStatus(tcmPidFile)
	if tcm.Code == process_utils.ProcessRunningCode {
		return tcm
	}

	return watchdog
}

func internalTcmStatus(cmdCtx *cmdcontext.CmdCtx, args []string) error {
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

	status := tcmStatus()

	pid := ""
	if status.PID != 0 {
		pid = strconv.Itoa(status.PID)
	}

	statusTable.AppendRows([]table.Row{
		{"TCM", status.Status, pid},
	})
	statusTable.Render()

	return nil
}

func internalTcmStop(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	return stopTcm(process_utils.TerminationTimeout)
}

// stopTcm stops the watchdog of TCM, when one holds its pid file, and TCM
// started without a watchdog otherwise, as tcmStatus finds them.
func stopTcm(wait time.Duration) error {
	if isExists, _ := process_utils.ExistsAndRecord(watchdogPidFile); isExists {
		_, err := process_utils.StopProcessWithin(watchdogPidFile, wait)
		if err != nil {
			return err
		}

		log.Info("Watchdog and TCM stopped")
	} else {
		_, err := process_utils.StopProcessWithin(tcmPidFile, wait)
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
