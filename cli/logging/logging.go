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
	// coloured only on one. Nil means a probe of the file descriptor when
	// Writer is an *os.File.
	IsTerminal func(io.Writer) bool
	// LookupEnv reads the environment for NO_COLOR and TERM. Nil means
	// os.LookupEnv.
	LookupEnv func(key string) (string, bool)
}

// NewHandler builds the handler chain Options describe without installing
// it.
func NewHandler(opts Options) (slog.Handler, error) {
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
		inner = newTextHandler(opts.Writer, opts.Level, colorEnabled(opts))
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

// colorEnabled reports whether the text format should be coloured: the
// writer is a terminal, NO_COLOR is not set to a non-empty value
// (https://no-color.org) and TERM is not "dumb".
func colorEnabled(opts Options) bool {
	lookupEnv := opts.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	if value, ok := lookupEnv("NO_COLOR"); ok && value != "" {
		return false
	}

	if value, ok := lookupEnv("TERM"); ok && value == "dumb" {
		return false
	}

	isTerminal := opts.IsTerminal
	if isTerminal == nil {
		isTerminal = fileIsTerminal
	}

	return isTerminal(opts.Writer)
}

// fileIsTerminal reports whether writer is an *os.File open on a terminal.
func fileIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}

	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

// Fatalf logs a formatted message at Error level and exits with code 1.
//
// It exists for the few places that still end the process themselves
// instead of returning an error to the command's caller. Do not add callers:
// once every failure travels back to one exit point, this function goes.
func Fatalf(format string, args ...any) {
	sdklog.Errorf(format, args...)
	os.Exit(1)
}
