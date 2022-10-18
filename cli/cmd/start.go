package cmd

import (
	"os"
	"os/exec"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

var (
	// "watchdog" is a hidden flag used to daemonize a process.
	// In go, we can't just fork the process (reason - goroutines).
	// So, for daemonize, we restarts the process with "watchdog" flag.
	watchdog bool
)

// NewStartCmd creates start command.
func NewStartCmd() *cobra.Command {
	var startCmd = &cobra.Command{
		Use:   "start <APPLICATION_NAME>",
		Short: "Start tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalStartModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	startCmd.Flags().BoolVar(&watchdog, "watchdog", false, "")
	startCmd.Flags().MarkHidden("watchdog")

	return startCmd
}

// internalStartModule is a default start module.
func internalStartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	var runningCtx running.RunningCtx
	if err = running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	if !watchdog {
		ttBin, err := os.Executable()
		if err != nil {
			return err
		}
		for _, run := range runningCtx.Instances {
			log.Infof("Starting an instance [%s]...", run.InstName)

			appName := ""
			if run.SingleApp {
				appName = run.AppName
			} else {
				appName = run.AppName + ":" + run.InstName
			}

			newArgs := []string{"start", "--watchdog", appName}

			wdCmd := exec.Command(ttBin, newArgs...)
			wdCmd.Stdout = os.Stdout
			wdCmd.Stderr = os.Stderr

			if err := wdCmd.Start(); err != nil {
				return err
			}
		}

		return nil
	}

	if err := running.Start(cmdCtx, &runningCtx.Instances[0]); err != nil {
		return err
	}
	return nil
}
