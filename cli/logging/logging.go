// Package logging sets up the process logger of the tt core.
//
// tt logs through the github.com/tarantool/tt/sdk/log facade, which writes to
// slog.Default. This package builds the handler behind it and installs it:
// records below the configured level are dropped, every record has its
// secrets redacted with the process-wide sdk/log registry and the trailing
// line breaks of its message trimmed, and what is left is rendered as text
// for people or as JSON for machines. Logs go to stderr;
// stdout is left to command results.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/mattn/go-isatty"
	sdklog "github.com/tarantool/tt/sdk/log"
)

// Format is a log format. It implements pflag.Value, so it can back a flag
// directly: an unknown value is refused while the flags are parsed.
type Format string

const (
	// FormatText renders a record as one human-readable line.
	FormatText Format = "text"
	// FormatJSON renders a record as one JSON object per line, as
	// slog.JSONHandler does.
	FormatJSON Format = "json"
)

// ErrUnknownFormat is returned for a log format other than text or json.
var ErrUnknownFormat = errors.New("unknown log format")

// String returns the format name.
func (f *Format) String() string {
	return string(*f)
}

// Set parses value as a format name.
func (f *Format) Set(value string) error {
	format := Format(value)

	err := validateFormat(format)
	if err != nil {
		return err
	}

	*f = format

	return nil
}

// Type names the flag value type in the help output.
func (*Format) Type() string {
	return "text|json"
}

// validateFormat reports ErrUnknownFormat for anything but a known format.
func validateFormat(format Format) error {
	switch format {
	case FormatText, FormatJSON:
		return nil
	default:
		return fmt.Errorf("%w %q: use %q or %q",
			ErrUnknownFormat, string(format), FormatText, FormatJSON)
	}
}

// ErrNoWriter is returned when Options carries no writer.
var ErrNoWriter = errors.New("log writer is not set")

// Options configure the process logger.
type Options struct {
	// Level is the lowest level logged. The zero value is slog.LevelInfo.
	Level slog.Level
	// Format selects the rendering: FormatText or FormatJSON.
	Format Format
	// Writer receives the rendered records; tt passes os.Stderr.
	Writer io.Writer
	// Redactor masks secrets in every record. Nil means the process-wide
	// registry, sdklog.Secrets.
	Redactor *sdklog.Redactor
	// IsTerminal reports whether Writer is a terminal; the text format is
	// coloured and shows a spinner status line only on one. Nil means a
	// probe of the file descriptor when Writer is an *os.File.
	IsTerminal func(io.Writer) bool
	// LookupEnv reads the environment for NO_COLOR and TERM. Nil means
	// os.LookupEnv.
	LookupEnv func(key string) (string, bool)
}

// terminalHooks are how a handler measures its terminal and animates its
// spinner; tests replace them to make both deterministic.
type terminalHooks struct {
	width func(io.Writer) int
	ticks ticker
}

// NewHandler builds the handler chain Options describe without installing
// it.
//
// The text handler implements sdklog.StatusHandler, so sdklog.Spinner draws
// through it: on a terminal that can redraw a line the spinner is shown on
// the writer below the records, anywhere else it is not shown at all. The
// JSON handler shows no spinner.
func NewHandler(opts Options) (slog.Handler, error) {
	return newHandler(opts, terminalHooks{width: fileWidth, ticks: realTicker})
}

// newHandler builds the handler chain Options describe, measuring and
// animating the terminal with hooks.
func newHandler(opts Options, hooks terminalHooks) (slog.Handler, error) {
	err := validateFormat(opts.Format)
	if err != nil {
		return nil, err
	}

	if opts.Writer == nil {
		return nil, ErrNoWriter
	}

	redactor := opts.Redactor
	if redactor == nil {
		redactor = sdklog.Secrets()
	}

	var inner slog.Handler

	switch opts.Format {
	case FormatJSON:
		inner = slog.NewJSONHandler(opts.Writer, &slog.HandlerOptions{
			AddSource:   false,
			Level:       opts.Level,
			ReplaceAttr: nil,
		})
	case FormatText:
		lookupEnv := opts.LookupEnv
		if lookupEnv == nil {
			lookupEnv = os.LookupEnv
		}

		isTerminal := opts.IsTerminal
		if isTerminal == nil {
			isTerminal = fileIsTerminal
		}

		terminal := isTerminal(opts.Writer) && !dumbTerminal(lookupEnv)
		status := newStatusLine(opts.Writer, terminal, hooks.width, hooks.ticks)
		color := terminal && !noColor(lookupEnv)

		inner = newTextHandler(opts.Writer, opts.Level, color, status)
	}

	return newScrubHandler(inner, redactor), nil
}

// Setup builds the handler chain Options describe and makes it the handler
// of slog.Default. slog.SetDefault also routes the standard library's log
// package through it.
func Setup(opts Options) error {
	handler, err := NewHandler(opts)
	if err != nil {
		return err
	}

	slog.SetDefault(slog.New(handler))

	return nil
}

// noColor reports whether NO_COLOR is set to a non-empty value
// (https://no-color.org), which turns colour off.
func noColor(lookupEnv func(string) (string, bool)) bool {
	value, ok := lookupEnv("NO_COLOR")

	return ok && value != ""
}

// dumbTerminal reports whether TERM is "dumb": a terminal that takes
// neither colour nor the sequence that erases a line.
func dumbTerminal(lookupEnv func(string) (string, bool)) bool {
	value, ok := lookupEnv("TERM")

	return ok && value == "dumb"
}

// fileIsTerminal reports whether writer is an *os.File open on a terminal.
func fileIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}

	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}
