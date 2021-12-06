package cmd

import (
	"os"
	"os/exec"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/context"
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
		Use:   "start [APPLICATION_NAME]",
		Short: "Start tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&ctx, cmd.Name(), &modulesInfo, internalStartModule, args)
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
func internalStartModule(ctx *context.Ctx, args []string) error {
	cliOpts, err := modules.GetCliOpts(ctx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	if err = running.FillCtx(cliOpts, ctx, args); err != nil {
		return err
	}

	if !watchdog {
		ttBin, err := os.Executable()
		if err != nil {
			return err
		}
		newArgs := append([]string{"start", "--watchdog"}, args...)

		wdCmd := exec.Command(ttBin, newArgs...)
		wdCmd.Stdout = os.Stdout
		wdCmd.Stderr = os.Stderr
		if err := wdCmd.Start(); err != nil {
			return err
		}

		return nil
	}

	log.Info("Starting an instance...")
	if err = running.Start(ctx); err != nil {
		return err
	}

	return nil
}
