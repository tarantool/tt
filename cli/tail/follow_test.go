package tail_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tarantool/tt/cli/tail"
)

const (
	linesPerStep     = 3
	channelCapacity  = 100
	logLineFormat    = "%03d: line"
	logNewLineFormat = "%03d: new line added"
)

// readWithTimeout Helper to read from channel with timeout.
func readWithTimeout(t *testing.T, ch <-chan string, timeout time.Duration) (string, error) {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case s := <-ch:
		return s, nil
	case <-timer.C:
		return "", fmt.Errorf("timeout waiting for data")
	}
}

func writeLogLines(t *testing.T, f *os.File, count int, line_fmt string) error {
	for i := range count {
		_, err := fmt.Fprintln(f, fmt.Sprintf(line_fmt, i+1))
		if err != nil {
			t.Fatalf("Failed to write line %d: %v", i+1, err)
			return err
		}
	}

	return nil
}

func createTmpLogFile(t *testing.T, count int, line_fmt string) string {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "follow-test-*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer f.Close()

	if err = writeLogLines(t, f, count, line_fmt); err != nil {
		t.Fatalf("Failed to write initial log lines: %v", err)
	}

	return f.Name()
}

func checksLinesInFile(t *testing.T, lines int, ch <-chan string, exp_fmt string) error {
	t.Helper()

	for i := range lines {
		n := i + 1

		line, err := readWithTimeout(t, ch, time.Second)
		if err != nil {
			return fmt.Errorf("failed to read line %d: %w", n, err)
		}

		expected := fmt.Sprintf(exp_fmt, n)
		if line != expected {
			return fmt.Errorf("line %d mismatch: got %q, want %q", n, line, expected)
		}
	}

	return nil
}

func TestFollow2_ReadExistingContent(t *testing.T) {
	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	if err != nil {
		t.Fatalf("Failed to follow: %v", err)
	}

	err = checksLinesInFile(t, linesPerStep, outCh, logLineFormat)
	if err != nil {
		t.Fatalf("Failed to check lines in file: %v", err)
	}

	cancel()
	f.Wait()
}

func TestFollow2_FollowNewContent(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping flaky test on CI until issue #TNTP-3131 is fixed")
	}

	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	if err != nil {
		t.Fatalf("Failed to follow: %v", err)
	}

	err = checksLinesInFile(t, linesPerStep, outCh, logLineFormat)
	if err != nil {
		t.Fatalf("Failed to check lines in file: %v", err)
	}

	// Append new content
	appendFile, err := os.OpenFile(lf, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Failed to open file for append: %v", err)
	}

	err = writeLogLines(t, appendFile, linesPerStep, logNewLineFormat)
	if err != nil {
		t.Fatalf("Failed to write new log lines: %v", err)
	}

	appendFile.Close()

	err = checksLinesInFile(t, linesPerStep, outCh, logNewLineFormat)
	if err != nil {
		t.Fatalf("Failed to check lines in file: %v", err)
	}

	cancel()
	f.Wait()
}

func TestFollow2_ContextCancellation(t *testing.T) {
	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	if err != nil {
		t.Fatalf("Failed to follow: %v", err)
	}

	err = checksLinesInFile(t, linesPerStep, outCh, logLineFormat)
	if err != nil {
		t.Fatalf("Failed to check lines in file: %v", err)
	}

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
		t.Fatal("Timeout waiting for Follow2 goroutine to terminate after context cancellation")
	}
}

func TestFollow2_NonExistentFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower("/path/to/nonexistent/file")

	_, err := f.Follow(ctx, linesPerStep)
	if err == nil {
		t.Fatal("Expected error when following non-existent file, got nil")
	}
}

func rotationTest(t *testing.T, use_delay bool) {
	lf := createTmpLogFile(t, linesPerStep, logLineFormat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f := tail.NewTailFollower(lf)

	outCh, err := f.Follow(ctx, linesPerStep)
	if err != nil {
		t.Skipf("Failed to follow: %v", err)
	}

	err = checksLinesInFile(t, linesPerStep, outCh, logLineFormat)
	if err != nil {
		t.Skipf("Failed to check initial lines in file: %v", err)
		return
	}

	err = os.Rename(lf, lf+".bak")
	if err != nil {
		t.Fatalf("Failed to rotate log file: %v", err)
	}

	if use_delay {
		time.Sleep(500 * time.Millisecond) // Add delay to avoid flaky fails.
	}

	newFile, err := os.Create(lf)
	if err != nil {
		t.Fatalf("Failed to create new log file: %v", err)
	}

	err = writeLogLines(t, newFile, linesPerStep, logNewLineFormat)

	newFile.Close()

	if err != nil {
		t.Skipf("Failed to write new log lines after rotation: %v", err)
		return
	}

	newFile.Close()

	err = checksLinesInFile(t, linesPerStep, outCh, logNewLineFormat)
	if err != nil {
		t.Skipf("Failed to check appended lines in file: %v", err)
		return
	}

	cancel()
	f.Wait()
}

// TestFollow2_FileRotation_Flaky tests the file rotation with flaky retries.
// It retries the test multiple times to handle potential flakiness in the tail library.
// This is a workaround for the issue #TNTP-3131, where the tail library
// does not handle file rotation correctly.
//   - TODO: Need fix `tail` library, see #TNTP-3131 for more details.
func TestFollow2_FileRotation_Flaky(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping flaky test on CI until issue #TNTP-3131 is fixed")
	}

	const flakyRepeatCount = 3

	test_pass := false

	for i := range flakyRepeatCount {
		t.Run(fmt.Sprintf("Rotation-%d", i+1), func(t *testing.T) {
			rotationTest(t, i > 0)

			test_pass = !t.Skipped()
		})

		if test_pass {
			break
		}

		t.Logf("FLAKY test %s failed, retrying flaky test iteration", t.Name())
	}

	if !test_pass {
		t.Fatalf("Test failed after all flaky iterations")
	}
}
