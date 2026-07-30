package tail_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/tail"
)

const (
	linesPerStep      = 3
	channelCapacity   = 100
	lineReadTimeout   = 5 * time.Second
	reopenWatchDelay  = 500 * time.Millisecond
	watcherStartDelay = 100 * time.Millisecond
	logLineFormat     = "%03d: line"
	logNewLineFormat  = "%03d: new line added"
)

// readWithTimeout Helper to read from channel with timeout.
//
//nolint:unparam // the wait is spelled out at every call site.
func readWithTimeout(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case s, ok := <-ch:
		require.True(t, ok, "channel closed while waiting for data")
		return s
	case <-timer.C:
		require.Fail(t, "timeout waiting for data")
		return ""
	}
}

func writeLogLines(t *testing.T, f *os.File, count int, line_fmt string) {
	t.Helper()

	for i := range count {
		_, err := fmt.Fprintln(f, fmt.Sprintf(line_fmt, i+1))
		require.NoErrorf(t, err, "failed to write line %d", i+1)
	}
}

func createTmpLogFile(t *testing.T, count int, line_fmt string) string {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "follow-test-*.log")
	require.NoError(t, err)
	defer f.Close()

	writeLogLines(t, f, count, line_fmt)

	return f.Name()
}

func appendSyncedLine(t *testing.T, path, line string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer file.Close()

	_, err = fmt.Fprintln(file, line)
	require.NoError(t, err)
	require.NoError(t, file.Sync())
}

// checksLinesInFile reads and checks the given number of lines from
// the channel, the count is spelled out at every call site.
//
//nolint:unparam // the count belongs in the test, not in the helper.
func checksLinesInFile(t *testing.T, lines int, ch <-chan string, exp_fmt string) {
	t.Helper()

	for i := range lines {
		n := i + 1

		line := readWithTimeout(t, ch, lineReadTimeout)
		require.Equal(t, fmt.Sprintf(exp_fmt, n), line)
	}
}

func TestFollow2_ReadExistingContent(t *testing.T) {
	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	cancel()
	f.Wait()
}

func TestFollow2_FollowNewContent(t *testing.T) {
	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	// TailFile starts its filesystem watcher asynchronously. Keep that startup
	// outside the append scenario this test exercises.
	time.Sleep(watcherStartDelay)

	// Append new content
	appendFile, err := os.OpenFile(lf, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)

	writeLogLines(t, appendFile, linesPerStep, logNewLineFormat)

	appendFile.Close()

	checksLinesInFile(t, linesPerStep, outCh, logNewLineFormat)

	cancel()
	f.Wait()
}

func TestFollow2_ContextCancellation(t *testing.T) {
	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	// Cancel context and wait for goroutine to finish
	cancel()

	// Wait with timeout to ensure goroutine completes
	waitCh := make(chan struct{})
	go func() {
		f.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		// Success - goroutine completed

	case <-time.After(3 * time.Second):
		require.Fail(t,
			"timeout waiting for Follow2 goroutine to terminate after context cancellation")
	}
}

func TestFollow2_NonExistentFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower("/path/to/nonexistent/file")

	_, err := f.Follow(ctx, linesPerStep)
	require.Error(t, err)
}

func TestFollow2_FileRotation(t *testing.T) {
	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	defer func() {
		cancel()
		f.Wait()
	}()

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	// TailFile starts its filesystem watcher asynchronously. Keep that startup
	// outside the write-plus-rename scenario this test exercises.
	time.Sleep(watcherStartDelay)

	const readinessLine = "watcher is ready"
	appendSyncedLine(t, lf, readinessLine)
	require.Equal(t, readinessLine, readWithTimeout(t, outCh, lineReadTimeout))

	const replacementLine = "line in replacement file"
	replacementPath := lf + ".new"
	require.NoError(t, os.WriteFile(replacementPath, []byte(replacementLine+"\n"), 0o644))

	const lineBeforeRotation = "line written immediately before rotation"
	appendSyncedLine(t, lf, lineBeforeRotation)
	require.NoError(t, os.Rename(lf, lf+".bak"))

	require.Equal(t, lineBeforeRotation, readWithTimeout(t, outCh, lineReadTimeout))

	// Let the asynchronous reopen path start watching for the replacement.
	// The loss assertion above remains adjacent to the rename.
	time.Sleep(reopenWatchDelay)
	require.NoError(t, os.Rename(replacementPath, lf))

	require.Equal(t, replacementLine, readWithTimeout(t, outCh, lineReadTimeout))

	cancel()
	f.Wait()

	for line := range outCh {
		require.Fail(t, "unexpected line after rotation", line)
	}
}
