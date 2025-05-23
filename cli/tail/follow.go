package tail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/apex/log"
	"github.com/nxadm/tail"
)

const (
	linesChannelCapacity = 64
	maxRetriesReopen     = 5
	retryOpenDelay       = 300 * time.Millisecond
)

// Follower is an interface for reading and following log files.
type Follower interface {
	// Follow reads the last `lines` lines from the log file and prints them.
	// It continues to follow the log file, printing new lines as they are added,
	// keeps working until context `Done`.
	Follow(ctx context.Context, lines int) (<-chan string, error)

	// Wait waits for the follower to finish processing.
	Wait()
}

type fileFollower struct {
	name       string
	wg         sync.WaitGroup
	followDone chan struct{}
}

// NewTailFollower creates a new Follower that handle file with [tail] library.
func NewTailFollower(fileName string) Follower {
	return &fileFollower{
		name:       fileName,
		followDone: make(chan struct{}),
	}
}

// Follow implements the Follower interface.
func (f *fileFollower) Follow(ctx context.Context, lines int) (<-chan string, error) {
	out := make(chan string, linesChannelCapacity)

	if err := f.startFollowing(ctx, out, lines); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("not found file %q", f.name)
		}

		return nil, fmt.Errorf("cannot read file %q: %w", f.name, err)
	}

	go func() {
		defer close(f.followDone)
		f.wg.Wait()
		close(out)
	}()

	return out, nil
}

// Wait implements the Follower interface.
func (f *fileFollower) Wait() {
	<-f.followDone
}

func (f *fileFollower) tryReopenTailer(ctx context.Context, cfg *tail.Config) (*tail.Tail, error) {
	newCfg := tail.Config{
		Location:      nil,
		MustExist:     true,
		Follow:        true,
		ReOpen:        true,
		CompleteLines: cfg.CompleteLines,
		Logger:        cfg.Logger,
		Poll:          cfg.Poll,
	}

	if _, err := os.Stat(f.name); errors.Is(err, os.ErrNotExist) {
		log.Infof("tryReopenTailer: file %q does not exist", f.name)
	}

	for i := range maxRetriesReopen {
		timer := time.NewTimer(retryOpenDelay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context (%w) while waiting to retry for %q",
				ctx.Err(), f.name)

		case <-timer.C:
			log.Infof("Wake up to retry tailing %q", f.name)
		}

		newT, err := tail.TailFile(f.name, newCfg)
		if err == nil {
			log.Infof("Successfully re-established tailing for %q, after %d retries", f.name, i)
			return newT, nil
		}

		log.Warnf("Retry(%d) for %q: failed to re-initialize tailer: %v.", i, f.name, err)
	}

	return nil, fmt.Errorf("failed to re-establish tailing for %q after %d retries",
		f.name, maxRetriesReopen)
}

func (f *fileFollower) handleTailerStopStatus(ctx context.Context, curT *tail.Tail) (
	*tail.Tail, error,
) {
	stopErr := curT.Stop()

	if ctx.Err() != nil {
		return nil, fmt.Errorf("context (%w) while tailing %q", ctx.Err(), f.name)
	}

	if stopErr != nil && errors.Is(stopErr, os.ErrNotExist) {
		t, err := f.tryReopenTailer(ctx, &curT.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to reopen tailer for %q: %w", f.name, err)
		}

		if t == nil || t.Lines == nil {
			return nil, fmt.Errorf("tailer for %q is nil after reopening", f.name)
		}

		if ctx.Err() != nil {
			t.Stop()
			return nil, fmt.Errorf("context (%w) while reopening tailer %q",
				ctx.Err(), f.name)
		}

		return t, nil
	}

	return nil, fmt.Errorf("failed to stop tailer for %q: %w", f.name, stopErr)
}

func (f *fileFollower) followFile(ctx context.Context, t *tail.Tail, out chan<- string) {
	defer f.wg.Done()

	for {
		if t == nil || t.Lines == nil {
			log.Errorf("Tailer or its Lines channel is nil for %s", f.name)
			return
		}

		select {
		case <-ctx.Done():
			log.Infof("Context cancelled. Stopping tailing of %q.", f.name)

			if err := t.Stop(); err != nil {
				log.Infof("Error stopping tailer for %q on context cancellation: %v",
					f.name, err)
			}

			return

		case line, more := <-t.Lines:
			if !more {
				var err error

				log.Warnf(
					"Tailer for %q Lines channel closed. Attempting to stop tailer.",
					f.name)

				t, err = f.handleTailerStopStatus(ctx, t)
				if err == nil {
					log.Infof("Reopened tailer for %q. Continuing to follow", f.name)

					continue
				}

				log.Errorf("Failed to handle tailer exit status for %q: %v", f.name, err)

				return
			}

			select {
			case out <- line.Text:
			case <-ctx.Done():
				log.Infof("Context cancelled while attempting to send line from %q. Stopping.",
					f.name)

				if stopErr := t.Stop(); stopErr != nil {
					log.Warnf(
						"Error stopping tailer for %q on context cancellation (during send): %v",
						f.name, stopErr)
				}

				return
			}
		}
	}
}

// startFollowing sends to the channel each new line from the file as it grows.
// It's a fork of the original Follow function, but it uses extra synchronization
// to solve the issue of the tailer library with rotated log files.
func (f *fileFollower) startFollowing(ctx context.Context, out chan<- string, lines int) error {
	file, err := os.Open(f.name)
	if err != nil {
		return fmt.Errorf("cannot open %q: %w", f.name, err)
	}
	defer file.Close()

	_, startPos, err := newTailReader(ctx, file, lines)
	if err != nil {
		return fmt.Errorf("follow: failed create tailer: %w", err)
	}

	tCfg := tail.Config{
		Location: &tail.SeekInfo{
			Offset: startPos,
			Whence: io.SeekStart,
		},
		MustExist: true,
		Follow:    true,
		ReOpen:    true,
		Logger:    tail.DiscardingLogger,
	}

	t, err := tail.TailFile(f.name, tCfg)
	if err != nil {
		return err
	}

	f.wg.Add(1)
	go f.followFile(ctx, t, out)

	return nil
}
