// Package printing is the tt core's side of
// [github.com/tarantool/tt/sdk/output]: it makes the Printer a command writes
// its result through, with the formats the SDK does not encode itself - YAML -
// and the terminal check that decides styling.
//
// YAML goes through [output.Normalize] before it is encoded, as JSON does, so
// one result decodes to the same data whichever of the two a script asks
// for. YAML on its own could do more - spell NaN and the infinities as .nan
// and .inf, keep the integer key 1 apart from the string key "1" - and a
// result that relies on that would read differently in JSON; normalising
// both is what keeps -o json and -o yaml interchangeable. It also covers a
// struct with no exported fields that only has a String form, which YAML,
// like JSON, would write as {}.
//
// The YAML library honours encoding.TextMarshaler, so a value that marshals
// itself as text is written as that text, as in JSON. A []byte is the one
// value the two formats still write differently: JSON carries it as base64,
// YAML as a sequence of integers.
package printing

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/output"
)

// FormatYAML is YAML at a two-space indent; a stream of results is a
// sequence of YAML documents.
const FormatYAML output.Format = "yaml"

// yamlIndent is tt's usual YAML indent.
const yamlIndent = 2

// Formats are the formats every result-printing tt command accepts, in the
// order the help text lists them.
func Formats() []output.Format {
	return []output.Format{output.FormatHuman, output.FormatJSON, FormatYAML}
}

// Options are the Printer options the core adds: the YAML encoders and the
// terminal probe.
func Options() []output.Option {
	return []output.Option{
		output.WithEncoder(FormatYAML, encodeYAML),
		output.WithStreamEncoder(FormatYAML, newYAMLStream),
		output.WithTerminalProbe(isTerminal),
	}
}

// NewPrinter returns a Printer writing to the process's streams in format.
func NewPrinter(format output.Format) (*output.Printer, error) {
	printer, err := output.NewPrinter(output.StdStreams(), format, Options()...)
	if err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}

	return printer, nil
}

// isTerminal reports whether w is an *os.File open on a terminal.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}

	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

// encodeYAML writes value, normalised, as one YAML document.
func encodeYAML(w io.Writer, value any) error {
	normalized, err := output.Normalize(value)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(yamlIndent)

	err = encoder.Encode(normalized)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	err = encoder.Close()
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	return nil
}

// yamlStream streams YAML documents, each item one document opened by a
// "---" line, so a reader can split the stream at every marker, the first
// included.
type yamlStream struct {
	w io.Writer
}

func newYAMLStream(w io.Writer) output.StreamEncoder {
	return yamlStream{w: w}
}

// documentStart opens every document of a stream.
const documentStart = "---\n"

// Encode writes item as one complete YAML document.
func (s yamlStream) Encode(item any) error {
	var doc bytes.Buffer

	doc.WriteString(documentStart)

	err := encodeYAML(&doc, item)
	if err != nil {
		return err
	}

	_, err = doc.WriteTo(s.w)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	return nil
}

// Close writes nothing: every document is complete when Encode returns.
func (yamlStream) Close() error {
	return nil
}
