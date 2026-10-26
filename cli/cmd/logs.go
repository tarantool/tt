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
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/tail"
)

var logOpts struct {
	nLines int  // How many lines to print.
	follow bool // Follow logs output.
}

const (
	defaultLogLines      = 10
	logRootCheckInterval = 100 * time.Millisecond
)

// NewLogsCmd creates the logs command of tt instance.
func NewLogsCmd() *cobra.Command {
	logsCmd := &cobra.Command{
		Use:   "logs " + instanceTargetsUsage + " [flags]",
		Short: `Get logs of instance(s)`,
		Long: "Print the last lines of tt.log of the instances, the log their output " +
			"goes to, whether they run in the background or in the foreground.",
		RunE: internalRunE(internalLogsModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Any)
		},
	}

	addInstanceFlags(logsCmd)

	logsCmd.Flags().IntVarP(&logOpts.nLines, "lines", "n", defaultLogLines,
		"Count of last lines to output")
	// No -f: it is --force.
	logsCmd.Flags().BoolVar(&logOpts.follow, "follow", false,
		"Output appended data as the log file grows")

	return logsCmd
}

func printLines(ctx context.Context, in <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // The interrupt is reported as is.
		case line, ok := <-in:
			if !ok {
				return nil
			}

			_, _ = fmt.Fprintln(os.Stdout, line)
		}
	}
}

func logRoot(inst running.InstanceCtx) string {
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
			_, err := os.Stat(root)
			if errors.Is(err, os.ErrNotExist) {
				cancel()

				return
			}
		}
	}
}

func getLogRootContext(
	ctx context.Context,
	root string,
	rootContexts map[string]context.Context,
) context.Context {
	if rootCtx, ok := rootContexts[root]; ok {
		return rootCtx
	}

	rootCtx, cancel := context.WithCancel(ctx)

	rootContexts[root] = rootCtx

	go monitorLogRoot(rootCtx, root, cancel)

	return rootCtx
}

func follow(instances []running.InstanceCtx, lineCount int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rootContexts := make(map[string]context.Context)

	nextColor := tail.DefaultColorPicker()
	color := nextColor()

	const logLinesChannelCapacity = 64

	logLines := make(chan string, logLinesChannelCapacity)
	tailRoutinesStarted := 0
	// Wait group to wait for completion of all log reading routines to close the channel once.
	var waitGroup sync.WaitGroup

	for _, inst := range instances {
		root := logRoot(inst)
		rootCtx := getLogRootContext(ctx, root, rootContexts)

		err := tail.Follow(rootCtx, logLines,
			tail.NewLogFormatter(running.GetAppInstanceName(inst)+": ", color),
			inst.Log, lineCount, &waitGroup)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			stop()

			return fmt.Errorf("cannot read log file %q: %w", inst.Log, err)
		}

		tailRoutinesStarted++

		color = nextColor()
	}

	if tailRoutinesStarted > 0 {
		go func() {
			waitGroup.Wait()
			close(logLines)
		}()

		return printLines(ctx, logLines)
	}

	return nil
}

func printLastN(instances []running.InstanceCtx, lineCount int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	nextColor := tail.DefaultColorPicker()
	color := nextColor()

	for _, inst := range instances {
		logLines, err := tail.TailN(ctx,
			tail.NewLogFormatter(running.GetAppInstanceName(inst)+": ", color), inst.Log, lineCount)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			stop()

			return fmt.Errorf("cannot read log file %q: %w", inst.Log, err)
		}

		err = printLines(ctx, logLines)
		if err != nil {
			return err
		}

		color = nextColor()
	}

	return nil
}

// internalLogsModule is a default logs module.
func internalLogsModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveProcesses(cmdCtx, args)
	if err != nil {
		return err
	}

	if logOpts.follow {
		return follow(instances, logOpts.nLines)
	}

	return printLastN(instances, logOpts.nLines)
}
