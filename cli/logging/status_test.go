package logging_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdklog "github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/logging"
)

// erase is what the status line writes to return to the first column and
// erase the line.
const erase = "\r\x1b[K"

// plain is a LookupEnv with NO_COLOR set, so that a terminal shows the
// spinner without colour.
func plain(key string) (string, bool) {
	if key == "NO_COLOR" {
		return "1", true
	}

	return "", false
}

// termBuffer is a writer standing in for a terminal: it keeps what is
// written and signals every write on writes, so a test can wait for a
// spinner frame drawn by another goroutine.
type termBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes chan struct{}
}

func newTermBuffer() *termBuffer {
	return &termBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}, writes: make(chan struct{}, 64)}
}

func (b *termBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written, err := b.buf.Write(data)

	select {
	case b.writes <- struct{}{}:
	default:
	}

	return written, err
}

func (b *termBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// drain forgets the writes signalled so far.
func (b *termBuffer) drain() {
	for {
		select {
		case <-b.writes:
		default:
			return
		}
	}
}

// terminalOptions returns text-format options writing to out as to a
// terminal with the environment lookupEnv.
func terminalOptions(out *termBuffer, lookupEnv func(string) (string, bool)) logging.Options {
	return logging.Options{
		Level:      slog.LevelInfo,
		Format:     logging.FormatText,
		Writer:     out,
		Redactor:   sdklog.NewRedactor(),
		IsTerminal: terminal,
		LookupEnv:  lookupEnv,
	}
}

// terminalLogger returns a logger over a text handler on a fake terminal of
// width columns, the terminal and the channel that advances the spinner.
func terminalLogger(
	t *testing.T, width int, lookupEnv func(string) (string, bool),
) (*slog.Logger, *termBuffer, chan time.Time) {
	t.Helper()

	out := newTermBuffer()
	ticks := make(chan time.Time)

	handler, err := logging.NewHandlerOnTerminal(terminalOptions(out, lookupEnv), width, ticks)
	require.NoError(t, err)

	return slog.New(handler), out, ticks
}

// tick advances the spinner and waits until it has drawn the next frame.
func tick(t *testing.T, out *termBuffer, ticks chan<- time.Time) {
	t.Helper()

	out.drain()

	ticks <- time.Now()

	select {
	case <-out.writes:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the spinner drew no frame")
	}
}

func TestSpinner_RecordClearsAndRedraws(t *testing.T) {
	logger, out, ticks := terminalLogger(t, 0, plain)

	stop := sdklog.SpinnerOf(logger, "building rocks")

	assert.Equal(t, erase+"   | building rocks", out.String())

	tick(t, out, ticks)
	logger.Info("step one", "k", "v")
	tick(t, out, ticks)
	logger.Warn("careful")
	stop()
	logger.Info("after")

	want := "" +
		erase + "   | building rocks" +
		erase + "   / building rocks" +
		erase + "   • step one k=v\n" + erase + "   / building rocks" +
		erase + "   - building rocks" +
		erase + "   ⚠ careful\n" + erase + "   - building rocks" +
		erase +
		"   • after\n"
	assert.Equal(t, want, out.String())
}

func TestSpinner_StopLeavesLineClean(t *testing.T) {
	logger, out, _ := terminalLogger(t, 0, plain)

	stop := sdklog.SpinnerOf(logger, "working")
	stop()
	stop()

	assert.Equal(t, erase+"   | working"+erase, out.String())
}

func TestSpinner_Color(t *testing.T) {
	logger, out, _ := terminalLogger(t, 0, noEnv)

	sdklog.SpinnerOf(logger, "working")()

	assert.Equal(t, erase+"   \x1b[1;34m|\x1b[0m working"+erase, out.String())
}

func TestSpinner_NoColorKeepsSpinner(t *testing.T) {
	logger, out, _ := terminalLogger(t, 0, plain)

	sdklog.SpinnerOf(logger, "working")()

	assert.Equal(t, erase+"   | working"+erase, out.String())
}

func TestSpinner_FitsTerminalWidth(t *testing.T) {
	logger, out, _ := terminalLogger(t, 20, plain)

	sdklog.SpinnerOf(logger, "a long message\nthat wraps")()

	line := strings.TrimSuffix(strings.TrimPrefix(out.String(), erase), erase)
	assert.Equal(t, "   | a long mess...", line)
	assert.Len(t, []rune(line), 19)
}

func TestSpinner_OneLine(t *testing.T) {
	logger, out, _ := terminalLogger(t, 0, plain)

	sdklog.SpinnerOf(logger, "two\nlines\r")()

	assert.Equal(t, erase+"   | two lines "+erase, out.String())
}

func TestSpinner_RedactsMessage(t *testing.T) {
	out := newTermBuffer()
	opts := terminalOptions(out, plain)
	opts.Redactor.Register("s3cr3t")

	handler, err := logging.NewHandlerOnTerminal(opts, 0, make(chan time.Time))
	require.NoError(t, err)

	sdklog.SpinnerOf(slog.New(handler), "fetch s3cr3t")()

	assert.Contains(t, out.String(), "fetch ")
	assert.NotContains(t, out.String(), "s3cr3t")
}

func TestSpinner_OneAtATime(t *testing.T) {
	logger, out, _ := terminalLogger(t, 0, plain)

	stop := sdklog.SpinnerOf(logger, "first")
	stopSecond := sdklog.SpinnerOf(logger, "second")
	stopSecond()
	logger.Info("still spinning")
	stop()

	want := erase + "   | first" +
		erase + "   • still spinning\n" + erase + "   | first" +
		erase
	assert.Equal(t, want, out.String())
}

func TestSpinner_ThroughDerivedLoggers(t *testing.T) {
	logger, out, _ := terminalLogger(t, 0, plain)

	library := sdklog.LibraryOf(logger.With("a", 1).WithGroup("g"), "lib")

	sdklog.SpinnerOf(library, "working")()

	assert.Equal(t, erase+"   | working"+erase, out.String())
}

func TestSpinner_Off(t *testing.T) {
	tests := map[string]logging.Options{
		"not a terminal": {
			Format: logging.FormatText, IsTerminal: notTerminal, LookupEnv: noEnv,
		},
		"dumb terminal": {
			Format:     logging.FormatText,
			IsTerminal: terminal,
			LookupEnv:  env(map[string]string{"TERM": "dumb"}),
		},
		"json": {Format: logging.FormatJSON, IsTerminal: terminal, LookupEnv: noEnv},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			out := newTermBuffer()

			opts.Writer = out
			opts.Redactor = sdklog.NewRedactor()

			ticks := make(chan time.Time, 1)

			handler, err := logging.NewHandlerOnTerminal(opts, 0, ticks)
			require.NoError(t, err)

			logger := slog.New(handler)

			stop := sdklog.SpinnerOf(logger, "working")

			ticks <- time.Now()

			logger.Info("record")
			stop()

			written := out.String()
			assert.Contains(t, written, "record")
			assert.NotContains(t, written, "\r")
			assert.NotContains(t, written, "\x1b")
			assert.NotContains(t, written, "working")
		})
	}
}

func TestSpinner_DefaultProbeOnBuffer(t *testing.T) {
	var buf bytes.Buffer

	handler, err := logging.NewHandler(logging.Options{
		Level:      slog.LevelInfo,
		Format:     logging.FormatText,
		Writer:     &buf,
		Redactor:   sdklog.NewRedactor(),
		IsTerminal: nil,
		LookupEnv:  noEnv,
	})
	require.NoError(t, err)

	sdklog.SpinnerOf(slog.New(handler), "working")()

	assert.Empty(t, buf.String())
}

func TestSpinner_ProcessLogger(t *testing.T) {
	previous := slog.Default()

	t.Cleanup(func() { slog.SetDefault(previous) })

	out := newTermBuffer()

	handler, err := logging.NewHandlerOnTerminal(
		terminalOptions(out, plain), 0, make(chan time.Time))
	require.NoError(t, err)

	slog.SetDefault(slog.New(handler))

	sdklog.Spinner("working")()

	assert.Equal(t, erase+"   | working"+erase, out.String())
}

// TestSpinner_Concurrent logs from several goroutines while spinners start,
// animate and stop, and checks that every record is written whole and the
// terminal is left with a clean line.
func TestSpinner_Concurrent(t *testing.T) {
	out := newTermBuffer()
	ticker := time.NewTicker(50 * time.Microsecond)

	t.Cleanup(ticker.Stop)

	handler, err := logging.NewHandlerOnTerminal(terminalOptions(out, plain), 80, ticker.C)
	require.NoError(t, err)

	logger := slog.New(handler)

	const (
		loggers = 8
		records = 50
	)

	var group sync.WaitGroup

	for worker := range loggers {
		group.Go(func() {
			for index := range records {
				logger.Info(fmt.Sprintf("record %d-%d", worker, index))
			}
		})
	}

	for range 4 {
		group.Go(func() {
			for range 10 {
				stop := sdklog.SpinnerOf(logger, "working")

				time.Sleep(time.Millisecond)
				stop()
			}
		})
	}

	group.Wait()

	written := out.String()

	for worker := range loggers {
		for index := range records {
			line := fmt.Sprintf("   • record %d-%d\n", worker, index)
			assert.Equal(t, 1, strings.Count(written, line), line)
		}
	}

	assert.Contains(t, written, "working")
	assert.Contains(t, written[strings.LastIndex(written, "working"):], erase,
		"the last spinner line is erased")
}
