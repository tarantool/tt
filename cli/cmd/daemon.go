package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/daemon"
	"github.com/tarantool/tt/cli/process_utils"
)

// NewDaemonCmd creates daemon command.
func NewDaemonCmd() *cobra.Command {
	var daemonCmd = &cobra.Command{
		Use:   "daemon",
		Short: "Perform manipulations with the tt daemon (experimental)",
	}

	var startCmd = &cobra.Command{
		Use:   "start",
		Short: "start tt daemon",
		Run:   TtModuleCmdRun(internalDaemonStartModule),
		Args:  cobra.ExactArgs(0),
	}

	var stopCmd = &cobra.Command{
		Use:   "stop",
		Short: "stop tt daemon",
		Run:   TtModuleCmdRun(internalDaemonStopModule),
		Args:  cobra.ExactArgs(0),
	}

	var statusCmd = &cobra.Command{
		Use:   "status",
		Short: "status of tt daemon",
		Run:   TtModuleCmdRun(internalDaemonStatusModule),
		Args:  cobra.ExactArgs(0),
	}

	var restartCmd = &cobra.Command{
		Use:   "restart",
		Short: "restart tt daemon",
		Run:   TtModuleCmdRun(internalDaemonRestartModule),
		Args:  cobra.ExactArgs(0),
	}

	daemonSubCommands := []*cobra.Command{
		startCmd,
		stopCmd,
		statusCmd,
		restartCmd,
	}

	for _, cmd := range daemonSubCommands {
		daemonCmd.AddCommand(cmd)
	}

	return daemonCmd
}

// internalDaemonRestartModule is a default restart module.
func internalDaemonRestartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if err := internalDaemonStopModule(cmdCtx, args); err != nil {
		return err
	}

	if err := internalDaemonStartModule(cmdCtx, args); err != nil {
		return err
	}

	return nil
}

// internalDaemonStartModule is a default start module.
func internalDaemonStartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts, err := configure.GetDaemonOpts(cmdCtx.Cli.DaemonCfgPath)
	if err != nil {
		return err
	}

	daemonCtx := daemon.NewDaemonCtx(opts)
	if err := daemon.RunHTTPServerOnBackground(daemonCtx); err != nil {
		log.Fatalf(err.Error())
	}

	return nil
}

// internalDaemonStopModule is a default stop module.
func internalDaemonStopModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts, err := configure.GetDaemonOpts(cmdCtx.Cli.DaemonCfgPath)
	if err != nil {
		return err
	}

	daemonCtx := daemon.NewDaemonCtx(opts)
	if err := daemon.StopDaemon(daemonCtx); err != nil {
		return err
	}

	return nil
}

// internalDaemonStatusModule is a default status module.
func internalDaemonStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	opts, err := configure.GetDaemonOpts(cmdCtx.Cli.DaemonCfgPath)
	if err != nil {
		return err
	}

	daemonCtx := daemon.NewDaemonCtx(opts)
	log.Info(process_utils.ProcessStatus(daemonCtx.PIDFile).String())

	return nil
}
