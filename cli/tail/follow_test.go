package tail_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/tail"
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
func readWithTimeout(t *testing.T, linesCh <-chan string, timeout time.Duration) string {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case s, ok := <-linesCh:
		require.True(t, ok, "channel closed while waiting for data")

		return s
	case <-timer.C:
		require.Fail(t, "timeout waiting for data")

		return ""
	}
}

func writeLogLines(t *testing.T, file *os.File, count int, lineFormat string) {
	t.Helper()

	for i := range count {
		_, err := fmt.Fprintln(file, fmt.Sprintf(lineFormat, i+1))
		require.NoErrorf(t, err, "failed to write line %d", i+1)
	}
}

//nolint:unparam // the count is spelled out at every call site.
func createTmpLogFile(t *testing.T, count int, lineFormat string) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "follow-test-*.log")
	require.NoError(t, err)

	defer func() { require.NoError(t, file.Close()) }()

	writeLogLines(t, file, count, lineFormat)

	return file.Name()
}

func appendSyncedLine(t *testing.T, path, line string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)

	defer func() { require.NoError(t, file.Close()) }()

	_, err = fmt.Fprintln(file, line)
	require.NoError(t, err)
	require.NoError(t, file.Sync())
}

// checksLinesInFile reads and checks the given number of lines from
// the channel, the count is spelled out at every call site.
//
//nolint:unparam // the count belongs in the test, not in the helper.
func checksLinesInFile(t *testing.T, lines int, linesCh <-chan string, expectedFormat string) {
	t.Helper()

	for i := range lines {
		n := i + 1

		line := readWithTimeout(t, linesCh, lineReadTimeout)
		require.Equal(t, fmt.Sprintf(expectedFormat, n), line)
	}
}

func TestFollow2_ReadExistingContent(t *testing.T) {
	logPath := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	follower := tail.NewTailFollower(logPath)

	outCh, err := follower.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	cancel()
	follower.Wait()
}

func TestFollow2_FollowNewContent(t *testing.T) {
	logPath := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	follower := tail.NewTailFollower(logPath)

	outCh, err := follower.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	// TailFile starts its filesystem watcher asynchronously. Keep that startup
	// outside the append scenario this test exercises.
	time.Sleep(watcherStartDelay)

	// Append new content.
	appendFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)

	writeLogLines(t, appendFile, linesPerStep, logNewLineFormat)

	require.NoError(t, appendFile.Close())

	checksLinesInFile(t, linesPerStep, outCh, logNewLineFormat)

	cancel()
	follower.Wait()
}

func TestFollow2_ContextCancellation(t *testing.T) {
	logPath := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	follower := tail.NewTailFollower(logPath)

	outCh, err := follower.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	// Cancel context and wait for goroutine to finish.
	cancel()

	// Wait with timeout to ensure goroutine completes.
	waitCh := make(chan struct{})

	go func() {
		follower.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		// Success - goroutine completed.

	case <-time.After(3 * time.Second):
		require.Fail(t,
			"timeout waiting for Follow2 goroutine to terminate after context cancellation")
	}
}

func TestFollow2_NonExistentFile(t *testing.T) {
	follower := tail.NewTailFollower("/path/to/nonexistent/file")

	_, err := follower.Follow(t.Context(), linesPerStep)
	require.Error(t, err)
}

func TestFollow2_FileRotation(t *testing.T) {
	logPath := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())

	follower := tail.NewTailFollower(logPath)

	outCh, err := follower.Follow(ctx, linesPerStep)
	require.NoError(t, err)

	defer func() {
		cancel()
		follower.Wait()
	}()

	checksLinesInFile(t, linesPerStep, outCh, logLineFormat)

	// TailFile starts its filesystem watcher asynchronously. Keep that startup
	// outside the write-plus-rename scenario this test exercises.
	time.Sleep(watcherStartDelay)

	const readinessLine = "watcher is ready"

	appendSyncedLine(t, logPath, readinessLine)
	require.Equal(t, readinessLine, readWithTimeout(t, outCh, lineReadTimeout))

	const replacementLine = "line in replacement file"

	replacementPath := logPath + ".new"

	require.NoError(t, os.WriteFile(replacementPath, []byte(replacementLine+"\n"), 0o644))

	const lineBeforeRotation = "line written immediately before rotation"

	appendSyncedLine(t, logPath, lineBeforeRotation)
	require.NoError(t, os.Rename(logPath, logPath+".bak"))

	require.Equal(t, lineBeforeRotation, readWithTimeout(t, outCh, lineReadTimeout))

	// Let the asynchronous reopen path start watching for the replacement.
	// The loss assertion above remains adjacent to the rename.
	time.Sleep(reopenWatchDelay)
	require.NoError(t, os.Rename(replacementPath, logPath))

	require.Equal(t, replacementLine, readWithTimeout(t, outCh, lineReadTimeout))

	cancel()
	follower.Wait()

	for line := range outCh {
		require.Fail(t, "unexpected line after rotation", line)
	}
}
