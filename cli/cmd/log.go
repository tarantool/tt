package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/tail"
)

var logOpts struct {
	nLines int  // How many lines to print.
	follow bool // Follow logs output.
}

const logRootCheckInterval = 100 * time.Millisecond

// NewLogCmd creates log command.
func NewLogCmd() *cobra.Command {
	logCmd := &cobra.Command{
		Use:   "log [<APP_NAME> | <APP_NAME:INSTANCE_NAME>] [flags]",
		Short: `Get logs of instance(s)`,
		Run:   RunModuleFunc(internalLogModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
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

type logRootContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func logRoot(inst running.InstanceCtx) string {
	if inst.SingleApp {
		return inst.LogDir
	}
	return filepath.Dir(inst.LogDir)
}

func monitorLogRoot(ctx context.Context, root string, cancel context.CancelFunc) {
	ticker := time.NewTicker(logRootCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
				cancel()
				return
			}
		}
	}
}

func follow(instances []running.InstanceCtx, n int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rootContexts := make(map[string]logRootContext)
	defer func() {
		for _, rootCtx := range rootContexts {
			rootCtx.cancel()
		}
	}()

	nextColor := tail.DefaultColorPicker()
	color := nextColor()
	const logLinesChannelCapacity = 64
	logLines := make(chan string, logLinesChannelCapacity)
	tailRoutinesStarted := 0
	// Wait group to wait for completion of all log reading routines to close the channel once.
	var wg sync.WaitGroup
	for _, inst := range instances {
		root := logRoot(inst)
		rootCtx, ok := rootContexts[root]
		if !ok {
			rootCtx.ctx, rootCtx.cancel = context.WithCancel(ctx)
			rootContexts[root] = rootCtx
			go monitorLogRoot(rootCtx.ctx, root, rootCtx.cancel)
		}

		err := tail.Follow(rootCtx.ctx, logLines,
			tail.NewLogFormatter(running.GetAppInstanceName(inst)+": ", color),
			inst.Log, n, &wg)
		if err != nil {
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
		go func() {
			wg.Wait()
			close(logLines)
		}()
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
	err = running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, running.ConfigLoadCluster)
	if err != nil {
		return err
	}

	if logOpts.follow {
		return follow(runningCtx.Instances, logOpts.nLines)
	}

	return printLastN(runningCtx.Instances, logOpts.nLines)
}
