package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/tail"
	"github.com/tarantool/tt/cli/util"
)

var logOpts struct {
	nLines int  // How many lines to print.
	follow bool // Follow logs output.
}

// NewLogCmd creates log command.
func NewLogCmd() *cobra.Command {
	var logCmd = &cobra.Command{
		Use:   "log [<APP_NAME> | <APP_NAME:INSTANCE_NAME>] [flags]",
		Short: `Get logs of instance(s)`,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalLogModule, args)
			util.HandleCmdErr(cmd, err)
		},
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractAppNames,
				running.ExtractInstanceNames)
		},
	}

	logCmd.Flags().IntVarP(&logOpts.nLines, "lines", "n", 10,
		"Count of last lines to output")
	logCmd.Flags().BoolVarP(&logOpts.follow, "follow", "f", false,
		"Output appended data as the log file grows")

	return logCmd
}

func printLines(ctx context.Context, in <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-in:
			if !ok {
				return nil
			}
			fmt.Println(line)
		}
	}
}

func follow(instances []running.InstanceCtx, n int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	nextColor := tail.DefaultColorPicker()
	color := nextColor()
	const logLinesChannelCapacity = 64
	logLines := make(chan string, logLinesChannelCapacity)
	tailRoutinesStarted := 0
	for _, inst := range instances {
		if err := tail.Follow(ctx, logLines,
			tail.NewLogFormatter(running.GetAppInstanceName(inst)+": ", color),
			inst.Log, n); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			stop()
			return fmt.Errorf("cannot read log file %q: %s", inst.Log, err)
		}
		tailRoutinesStarted++
		color = nextColor()
	}

	if tailRoutinesStarted > 0 {
		return printLines(ctx, logLines)
	}
	return nil
}

func printLastN(instances []running.InstanceCtx, n int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	nextColor := tail.DefaultColorPicker()
	color := nextColor()
	for _, inst := range instances {
		logLines, err := tail.TailN(ctx,
			tail.NewLogFormatter(running.GetAppInstanceName(inst)+": ", color), inst.Log, n)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			stop()
			return fmt.Errorf("cannot read log file %q: %s", inst.Log, err)
		}
		if err := printLines(ctx, logLines); err != nil {
			return err
		}
		color = nextColor()
	}
	return nil
}

// internalLogModule is a default log module.
func internalLogModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var err error
	var runningCtx running.RunningCtx
	if err = running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	if logOpts.follow {
		return follow(runningCtx.Instances, logOpts.nLines)
	}

	return printLastN(runningCtx.Instances, logOpts.nLines)
}
