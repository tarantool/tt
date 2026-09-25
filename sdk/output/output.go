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
// The format is chosen by the --format flag alone: a command declares the
// flag with [BindFormat], which takes the formats it accepts and its fixed
// default, and [ResolveFormat] maps a flag value to a format. Whether stdout
// is a terminal never
// changes the format; it may change only styling - colour, width,
// truncation - and a command reads it from [Printer.Terminal].
//
// The SDK encodes JSON itself. Any other machine format is supplied by the tt
// core with [WithEncoder], which keeps its encoder library out of the SDK.
//
// A result too large to hold - the tuples of a big space, records read from
// xlogs, a log being followed - is written item by item through a [Stream]
// from [Printer.Stream]. In JSON a stream is JSON Lines: one compact value
// per line, no enclosing array. In the human format each item renders itself
// with [Result.Human], so columns cannot be aligned across items: an item
// that is a table row pads its cells to fixed widths, and a heading or a
// summary is printed with Print before the stream opens or after it closes.
// Another machine format streams only if the core gives it a stream encoder
// with [WithStreamEncoder]; YAML's stream, say, is a sequence of documents.
//
// A stream is not all-or-nothing. Each item is written whole or not at all,
// and as soon as it is emitted; when a command fails midway the items
// already written stay on stdout, the error goes to stderr and into the exit
// code, and the exit code is how a consumer tells a complete stream from a
// cut one. While a stream is open, Print, Printf, Emit and Stream refuse with
// ErrStreamOpen, so nothing interleaves with the items.
//
// The JSON encoder passes every value through [Normalize] first, so a result
// may carry what a MessagePack decoder produced - maps keyed by interfaces,
// integers or bools - without the command converting it. A core encoder
// calls Normalize itself when its format needs it.
//
// A float that is NaN, +Inf or -Inf has no JSON number, so it is written as
// the string "NaN", "Infinity" or "-Infinity", while a finite float stays a
// number: the JSON type of such a value depends on the value. This is how
// encoding/json/v2 writes a float under `json:",format:nonfinite"` and how
// the proto3 JSON mapping writes a double.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sync"
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
	// ErrNoStreamForm reports a stream requested in a machine format that
	// has no stream encoder.
	ErrNoStreamForm = errors.New("output format cannot be streamed")
	// ErrStreamOpen reports output attempted on a Printer while one of its
	// streams is open.
	ErrStreamOpen = errors.New("an output stream is open")
	// ErrStreamClosed reports an item emitted to a closed stream.
	ErrStreamClosed = errors.New("output stream is closed")
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

// StreamEncoder writes the items of one stream in a machine format.
//
// Encode writes one item; Close ends the stream and writes whatever the
// format puts after the last item. The writer a StreamEncoder is given
// collects what one call writes: what Encode wrote reaches stdout when it
// returns nil, and is discarded when it fails. Encode is not called
// concurrently.
type StreamEncoder interface {
	Encode(item any) error
	Close() error
}

// NewStreamEncoder starts a stream encoder writing to w. Anything it writes
// before returning - a header - reaches stdout when the stream opens.
type NewStreamEncoder func(w io.Writer) StreamEncoder

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

// WithStreamEncoder lets format be streamed by the encoders newEncoder
// starts, replacing any stream encoder the format already has. The format
// must also have an encoder, built in or given by WithEncoder. JSON streams
// out of the box; the core adds streaming to the formats it registers, such
// as YAML.
func WithStreamEncoder(format Format, newEncoder NewStreamEncoder) Option {
	return func(p *Printer) {
		p.streamEncoders[format] = newEncoder
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
	streams        Streams
	format         Format
	encoders       map[Format]Encoder
	streamEncoders map[Format]NewStreamEncoder
	probe          TerminalProbe
	terminal       bool

	// mu guards streaming, which a Stream closed on another goroutine
	// clears.
	mu        sync.Mutex
	streaming bool
}

// NewPrinter returns a Printer that writes to streams.Out in format. The
// format must be FormatHuman, FormatJSON, or one given an encoder by opts.
func NewPrinter(streams Streams, format Format, opts ...Option) (*Printer, error) {
	if streams.Out == nil {
		return nil, ErrNoStream
	}

	printer := &Printer{
		streams:        streams,
		format:         format,
		encoders:       map[Format]Encoder{FormatJSON: encodeJSON},
		streamEncoders: map[Format]NewStreamEncoder{FormatJSON: newJSONLines},
		probe:          nil,
		terminal:       false,
		mu:             sync.Mutex{},
		streaming:      false,
	}

	for _, opt := range opts {
		opt(printer)
	}

	if _, ok := printer.encoders[FormatHuman]; ok {
		return nil, fmt.Errorf("%w: an encoder for the human format", ErrUnknownFormat)
	}

	if _, ok := printer.streamEncoders[FormatHuman]; ok {
		return nil, fmt.Errorf("%w: a stream encoder for the human format", ErrUnknownFormat)
	}

	for _, streamed := range slices.Sorted(maps.Keys(printer.streamEncoders)) {
		if printer.streamEncoders[streamed] != nil && printer.encoders[streamed] == nil {
			return nil, fmt.Errorf("%w: a stream encoder for %q, which has no encoder",
				ErrUnknownFormat, streamed)
		}
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

// Writer returns stdout itself, for output the Printer does not shape -
// bytes passed through as they are. The command is then responsible for
// writing the chosen format; records written as they are read go through
// Stream instead. Writes through Writer are not checked against an open
// stream: keeping them apart is the caller's job.
func (p *Printer) Writer() io.Writer {
	return p.streams.Out
}

// idle returns ErrStreamOpen while a stream of the Printer is open.
func (p *Printer) idle() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streaming {
		return ErrStreamOpen
	}

	return nil
}

// Print writes human text to stdout, formatted as fmt.Fprint does. It is for
// a result that exists only as text; in a machine format it writes nothing
// and returns ErrNoMachineForm.
func (p *Printer) Print(args ...any) error {
	err := p.idle()
	if err != nil {
		return err
	}

	if p.format != FormatHuman {
		return fmt.Errorf("%w %q", ErrNoMachineForm, p.format)
	}

	_, err = fmt.Fprint(p.streams.Out, args...)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// Printf writes human text to stdout, formatted as fmt.Fprintf does. Like
// Print, it returns ErrNoMachineForm in a machine format.
func (p *Printer) Printf(format string, args ...any) error {
	err := p.idle()
	if err != nil {
		return err
	}

	if p.format != FormatHuman {
		return fmt.Errorf("%w %q", ErrNoMachineForm, p.format)
	}

	_, err = fmt.Fprintf(p.streams.Out, format, args...)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// Emit renders result to stdout: its Human rendering in the human format,
// the encoded value otherwise. A machine format is encoded in full before
// anything is written, so an encoding error leaves stdout untouched.
func (p *Printer) Emit(result Result) error {
	err := p.idle()
	if err != nil {
		return err
	}

	if p.format == FormatHuman {
		err = result.Human(p.streams.Out)
		if err != nil {
			return fmt.Errorf("rendering output: %w", err)
		}

		return nil
	}

	var buf bytes.Buffer

	err = p.encoders[p.format](&buf, result)
	if err != nil {
		return fmt.Errorf("encoding output as %s: %w", p.format, err)
	}

	_, err = buf.WriteTo(p.streams.Out)
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// encodeJSON writes value, normalised, as JSON indented by two spaces with a
// trailing newline, readable both directly and piped into jq.
func encodeJSON(w io.Writer, value any) error {
	normalized, err := Normalize(value)
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	err = encoder.Encode(normalized)
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	return nil
}
