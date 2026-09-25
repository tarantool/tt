package printing_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/output"

	"github.com/tarantool/tt/v3/cli/printing"
)

// entry is a result shaped like tt's listings.
type entry struct {
	Name    string   `json:"name"              yaml:"name"`
	Version string   `json:"version,omitempty" yaml:"version,omitempty"`
	Tags    []string `json:"tags,omitempty"    yaml:"tags,omitempty"`
}

func (e entry) Human(w io.Writer) error {
	_, err := fmt.Fprintln(w, e.Name)

	return err
}

// anyResult carries an arbitrary value as a Result.
type anyResult struct {
	Value any `json:"value" yaml:"value"`
}

func (anyResult) Human(io.Writer) error { return nil }

// opaque has only a String form, like go-tarantool's datetime.Datetime.
type opaque struct{ unix int64 }

func (o opaque) String() string { return fmt.Sprintf("@%d", o.unix) }

// textual marshals itself as text.
type textual struct{ v int }

func (t textual) MarshalText() ([]byte, error) { return fmt.Appendf(nil, "text-%d", t.v), nil }

func newPrinter(t *testing.T, format output.Format) (*output.Printer, *bytes.Buffer) {
	t.Helper()

	var out bytes.Buffer

	printer, err := output.NewPrinter(output.Streams{In: nil, Out: &out, Err: io.Discard},
		format, printing.Options()...)
	require.NoError(t, err)

	return printer, &out
}

func TestFormatsAreTableJSONAndYAML(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []output.Format{"table", "json", "yaml"}, printing.Formats())
}

func TestYAMLIsTwoSpaceIndented(t *testing.T) {
	t.Parallel()

	printer, out := newPrinter(t, printing.FormatYAML)
	require.NoError(t, printer.Emit(entry{Name: "stat", Version: "0.3.2-1", Tags: []string{"a"}}))

	assert.Equal(t, "name: stat\nversion: 0.3.2-1\ntags:\n  - a\n", out.String())
}

func TestYAMLMatchesTheLibraryAtTheSameIndent(t *testing.T) {
	t.Parallel()

	value := entry{Name: "checks", Version: ">=3.0.0", Tags: []string{"x", "y: z"}}

	var want bytes.Buffer

	encoder := yaml.NewEncoder(&want)
	encoder.SetIndent(2)
	require.NoError(t, encoder.Encode(value))
	require.NoError(t, encoder.Close())

	printer, out := newPrinter(t, printing.FormatYAML)
	require.NoError(t, printer.Emit(value))

	assert.Equal(t, want.String(), out.String())
}

func TestYAMLIsNormalisedLikeJSON(t *testing.T) {
	t.Parallel()

	value := anyResult{Value: map[any]any{
		int64(1): "one",
		true:     "yes",
		"nan":    math.NaN(),
		"inf":    math.Inf(-1),
		"when":   opaque{unix: 42},
		"text":   textual{v: 7},
	}}

	yamlPrinter, yamlOut := newPrinter(t, printing.FormatYAML)
	require.NoError(t, yamlPrinter.Emit(value))

	jsonPrinter, jsonOut := newPrinter(t, output.FormatJSON)
	require.NoError(t, jsonPrinter.Emit(value))

	var fromYAML, fromJSON map[string]any

	require.NoError(t, yaml.Unmarshal(yamlOut.Bytes(), &fromYAML))
	require.NoError(t, yaml.Unmarshal(jsonOut.Bytes(), &fromJSON))

	assert.Equal(t, map[string]any{"value": map[string]any{
		"1":    "one",
		"true": "yes",
		"nan":  "NaN",
		"inf":  "-Infinity",
		"when": "@42",
		"text": "text-7",
	}}, fromYAML)
	assert.Equal(t, fromJSON, fromYAML)
}

func TestYAMLRefusesCollidingKeysAndWritesNothing(t *testing.T) {
	t.Parallel()

	printer, out := newPrinter(t, printing.FormatYAML)
	err := printer.Emit(anyResult{Value: map[any]any{int64(1): "a", "1": "b"}})

	require.ErrorIs(t, err, output.ErrKeyCollision)
	assert.Empty(t, out.String())
}

// sliceWriter records every write stdout gets.
type sliceWriter struct {
	writes []string
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, string(p))

	return len(p), nil
}

func TestYAMLStreamsOneDocumentPerItem(t *testing.T) {
	t.Parallel()

	var out sliceWriter

	printer, err := output.NewPrinter(output.Streams{In: nil, Out: &out, Err: io.Discard},
		printing.FormatYAML, printing.Options()...)
	require.NoError(t, err)

	stream, err := printer.Stream()
	require.NoError(t, err)
	require.NoError(t, stream.Emit(entry{Name: "a", Version: "1"}))
	require.NoError(t, stream.Emit(entry{Name: "b"}))
	require.NoError(t, stream.Close())

	assert.Equal(t, []string{
		"---\nname: a\nversion: \"1\"\n",
		"---\nname: b\n",
	}, out.writes)

	decoder := yaml.NewDecoder(strings.NewReader(strings.Join(out.writes, "")))

	var names []string

	for {
		var doc entry

		err = decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)

		names = append(names, doc.Name)
	}

	assert.Equal(t, []string{"a", "b"}, names)
}

func TestYAMLStreamItemThatFailsWritesNothing(t *testing.T) {
	t.Parallel()

	printer, out := newPrinter(t, printing.FormatYAML)

	stream, err := printer.Stream()
	require.NoError(t, err)

	err = stream.Emit(anyResult{Value: map[any]any{int64(1): "a", "1": "b"}})
	require.ErrorIs(t, err, output.ErrKeyCollision)
	require.NoError(t, stream.Emit(entry{Name: "after"}))
	require.NoError(t, stream.Close())

	assert.Equal(t, "---\nname: after\n", out.String())
}

func TestTerminalIsFalseOffAFile(t *testing.T) {
	t.Parallel()

	printer, _ := newPrinter(t, output.FormatHuman)

	assert.False(t, printer.Terminal())
}
