package tcm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/tail"
)

// FollowLogs reads the last `lines` lines from the log file and prints them.
// It continues to follow the log file, printing new lines as they are added.
func FollowLogs(follower tail.Follower, prt Printer, lines int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logLines, err := follower.Follow(ctx, lines)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Warnf("Log file does not found: %v", err)

			return nil
		}

		return fmt.Errorf("failed to follow logs: %w", err)
	}

	printErr := prt.Print(ctx, logLines)

	follower.Wait()

	return printErr
}

// TailLogs reads the last `lines` lines from the log file and prints them.
func TailLogs(t tail.Reader, prt Printer, lines int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logLines, err := t.Read(ctx, lines)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Warnf("Log file does not found: %v", err)

			return nil
		}

		return fmt.Errorf("failed to show tail logs: %w", err)
	}

	return prt.Print(ctx, logLines)
}
