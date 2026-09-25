// Package output writes a command's result to stdout.
//
// stdout carries the result and nothing else; diagnostics, progress and
// prompts go to stderr through [github.com/tarantool/tt/sdk/log]. The result
// format (--format) is independent of the log format (--log-format).
//
// A command renders its result through a [Printer] bound to the process
// streams and the format the user chose. The human format is the command's
// own rendering, [Result.Human]; a machine format encodes the result value.
// A machine-format result is written whole or not at all, so a command that
// fails leaves stdout empty - there is no error envelope; the error goes to
// stderr and into the exit code.
//
// The format is chosen by the --format flag alone: [ResolveFormat] takes the
// flag and the command's fixed default. Whether stdout is a terminal never
// changes the format; it may change only styling - colour, width,
// truncation - and a command reads it from [Printer.Terminal].
//
// The SDK encodes JSON itself. Any other machine format is supplied by the tt
// core with [WithEncoder], which keeps its encoder library out of the SDK.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Format names how a result is rendered.
type Format string

const (
	// FormatHuman is the command's own rendering for people: a table, a tree,
	// a sentence. It is spelled "table", as every tt command with a --format
	// flag spells its human format.
	FormatHuman Format = "table"
	// FormatJSON is indented JSON with a trailing newline.
	FormatJSON Format = "json"
)

// ResolveFormat returns the format a --format flag value selects: the value
// itself, or fallback - the command's default - when the flag is empty. It is
// a pure function of its arguments; in particular it does not depend on
// whether stdout is a terminal. Whether the result names a format the command
// can render is checked by [NewPrinter].
func ResolveFormat(flag string, fallback Format) Format {
	if flag == "" {
		return fallback
	}

	return Format(flag)
}

var (
	// ErrUnknownFormat reports a format that is neither human nor one the
	// Printer has an encoder for.
	ErrUnknownFormat = errors.New("unknown output format")
	// ErrNoMachineForm reports human-only text printed while a machine format
	// was chosen. Such output has no machine form: emit a Result instead.
	ErrNoMachineForm = errors.New("human-only output in a machine format")
	// ErrNoStream reports streams without a stdout.
	ErrNoStream = errors.New("no output stream")
)

// Streams are the standard streams a command works with.
type Streams struct {
	// In is where input and prompt answers are read from.
	In io.Reader
	// Out receives the command's result and nothing else.
	Out io.Writer
	// Err receives diagnostics, progress and prompts.
	Err io.Writer
}

// StdStreams returns the process's stdin, stdout and stderr.
func StdStreams() Streams {
	return Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}

// Result is a command's result.
//
// Human writes the human rendering to w; it writes nothing that is not the
// result, so a note about the result (a stale lock, say) is logged to stderr
// instead. A machine format encodes the Result value itself, so its exported
// fields and struct tags are the machine schema; tt's payloads use
// snake_case keys.
type Result interface {
	Human(w io.Writer) error
}

// Encoder writes value to w in a machine format.
type Encoder func(w io.Writer, value any) error

// Option configures a Printer.
type Option func(*Printer)

// WithEncoder makes format a machine format rendered by encoder, replacing
// any encoder the format already has. The core uses it to add formats the
// SDK does not encode itself, such as YAML.
func WithEncoder(format Format, encoder Encoder) Option {
	return func(p *Printer) {
		p.encoders[format] = encoder
	}
}

// TerminalProbe reports whether w is a terminal.
type TerminalProbe func(w io.Writer) bool

// WithTerminalProbe sets how the Printer finds out whether stdout is a
// terminal. The core passes its isatty check; tests pass a constant. Without
// a probe stdout counts as not a terminal, which selects plain styling.
func WithTerminalProbe(probe TerminalProbe) Option {
	return func(p *Printer) {
		p.probe = probe
	}
}

// Printer writes results to stdout in one format.
type Printer struct {
	streams  Streams
	format   Format
	encoders map[Format]Encoder
	probe    TerminalProbe
	terminal bool
}

// NewPrinter returns a Printer that writes to streams.Out in format. The
// format must be FormatHuman, FormatJSON, or one given an encoder by opts.
func NewPrinter(streams Streams, format Format, opts ...Option) (*Printer, error) {
	if streams.Out == nil {
		return nil, ErrNoStream
	}

	printer := &Printer{
		streams:  streams,
		format:   format,
		encoders: map[Format]Encoder{FormatJSON: encodeJSON},
		probe:    nil,
		terminal: false,
	}

	for _, opt := range opts {
		opt(printer)
	}

	if _, ok := printer.encoders[FormatHuman]; ok {
		return nil, fmt.Errorf("%w: an encoder for the human format", ErrUnknownFormat)
	}

	if format != FormatHuman && printer.encoders[format] == nil {
		return nil, fmt.Errorf("%w %q", ErrUnknownFormat, format)
	}

	if printer.probe != nil {
		printer.terminal = printer.probe(streams.Out)
	}

	return printer, nil
}

// Format returns the format the Printer renders in.
func (p *Printer) Format() Format {
	return p.format
}

// Terminal reports whether stdout is a terminal, as the probe given by
// WithTerminalProbe answered when the Printer was made. It is for styling a
// human rendering - colour, width, truncation - and never for choosing the
// format.
func (p *Printer) Terminal() bool {
	return p.terminal
}

// Writer returns stdout itself, for a result that is a stream rather than a
// value - records dumped as they are read, a log being followed. The command
// is then responsible for writing the chosen format.
func (p *Printer) Writer() io.Writer {
	return p.streams.Out
}

// Print writes human text to stdout, formatted as fmt.Fprint does. It is for
// a result that exists only as text; in a machine format it writes nothing
// and returns ErrNoMachineForm.
func (p *Printer) Print(args ...any) error {
	if p.format != FormatHuman {
		return fmt.Errorf("%w %q", ErrNoMachineForm, p.format)
	}

	_, err := fmt.Fprint(p.streams.Out, args...)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// Printf writes human text to stdout, formatted as fmt.Fprintf does. Like
// Print, it returns ErrNoMachineForm in a machine format.
func (p *Printer) Printf(format string, args ...any) error {
	if p.format != FormatHuman {
		return fmt.Errorf("%w %q", ErrNoMachineForm, p.format)
	}

	_, err := fmt.Fprintf(p.streams.Out, format, args...)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// Emit renders result to stdout: its Human rendering in the human format,
// the encoded value otherwise. A machine format is encoded in full before
// anything is written, so an encoding error leaves stdout untouched.
func (p *Printer) Emit(result Result) error {
	if p.format == FormatHuman {
		err := result.Human(p.streams.Out)
		if err != nil {
			return fmt.Errorf("rendering output: %w", err)
		}

		return nil
	}

	var buf bytes.Buffer

	err := p.encoders[p.format](&buf, result)
	if err != nil {
		return fmt.Errorf("encoding output as %s: %w", p.format, err)
	}

	_, err = buf.WriteTo(p.streams.Out)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// encodeJSON writes value as JSON indented by two spaces with a trailing
// newline, readable both directly and piped into jq.
func encodeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	err := encoder.Encode(value)
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	return nil
}
