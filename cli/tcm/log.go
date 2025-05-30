package tcm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/tail"
)

const LogFileName = "tcm.log"

// FollowLogs reads the last `lines` lines from the log file and prints them.
// It continues to follow the log file, printing new lines as they are added.
func FollowLogs(lines uint, prt LogPrinter) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	const logLinesChannelCapacity = 64
	logLines := make(chan string, logLinesChannelCapacity)

	// Wait group to wait for completion of all log reading routines to close the channel once.
	var wg sync.WaitGroup
	if err := tail.Follow(ctx, logLines, prt.Format, LogFileName, int(lines), &wg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Warnf("Log file %q does not exist, nothing to show", LogFileName)
			return nil
		}
		return fmt.Errorf("cannot read log file %q: %w", LogFileName, err)
	}

	go func() {
		wg.Wait()
		close(logLines)
	}()

	return prt.Print(ctx, logLines)
}

// PrintLogs reads the last `lines` lines from the log file and prints them.
func PrintLogs(lines uint, prt LogPrinter) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logLines, err := tail.TailN(ctx, prt.Format, LogFileName, int(lines))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Warnf("Log file %q does not exist, nothing to show", LogFileName)
			return nil
		}
		return fmt.Errorf("cannot read log file %q: %w", LogFileName, err)
	}

	return prt.Print(ctx, logLines)
}
