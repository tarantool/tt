package output_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/output"
)

var errBroken = errors.New("broken")

// listing is a result shaped like tt package list: a table for people, a
// snake_case document for machines.
type listing struct {
	Scope    string   `json:"scope"`
	Packages []string `json:"packages"`
}

func (l listing) Human(out io.Writer) error {
	if len(l.Packages) == 0 {
		_, err := fmt.Fprintf(out, "no packages installed in %s scope\n", l.Scope)

		return err
	}

	for _, name := range l.Packages {
		_, err := fmt.Fprintln(out, name)
		if err != nil {
			return err
		}
	}

	return nil
}

// brokenResult fails to render, and fails to encode as JSON.
type brokenResult struct{}

func (brokenResult) Human(io.Writer) error { return errBroken }

func (brokenResult) MarshalJSON() ([]byte, error) { return nil, errBroken }

// halfEncoder writes part of a document and then fails, the way a streaming
// encoder can.
func halfEncoder(w io.Writer, _ any) error {
	_, _ = io.WriteString(w, "scope: pro")

	return errBroken
}

// fakeYAML stands in for the YAML encoder the core registers.
func fakeYAML(w io.Writer, value any) error {
	_, err := fmt.Fprintf(w, "yaml: %+v\n", value)

	return err
}

func newStreams() (output.Streams, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer

	return output.Streams{In: nil, Out: &stdout, Err: &stderr}, &stdout, &stderr
}

func TestEmit(t *testing.T) {
	t.Parallel()

	result := listing{Scope: "project", Packages: []string{"crud", "vshard"}}

	tests := []struct {
		name   string
		format output.Format
		opts   []output.Option
		want   string
	}{
		{
			name:   "human",
			format: output.FormatHuman,
			opts:   nil,
			want:   "crud\nvshard\n",
		},
		{
			name:   "json",
			format: output.FormatJSON,
			opts:   nil,
			want: "{\n  \"scope\": \"project\",\n" +
				"  \"packages\": [\n    \"crud\",\n    \"vshard\"\n  ]\n}\n",
		},
		{
			name:   "registered encoder",
			format: "yaml",
			opts:   []output.Option{output.WithEncoder("yaml", fakeYAML)},
			want:   "yaml: {Scope:project Packages:[crud vshard]}\n",
		},
		{
			name:   "json encoder replaced",
			format: output.FormatJSON,
			opts:   []output.Option{output.WithEncoder(output.FormatJSON, fakeYAML)},
			want:   "yaml: {Scope:project Packages:[crud vshard]}\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			streams, stdout, stderr := newStreams()

			printer, err := output.NewPrinter(streams, test.format, test.opts...)
			require.NoError(t, err)
			assert.Equal(t, test.format, printer.Format())

			require.NoError(t, printer.Emit(result))
			assert.Equal(t, test.want, stdout.String())
			assert.Empty(t, stderr.String())
		})
	}
}

func TestEmitFailureLeavesStdoutEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format output.Format
		opts   []output.Option
		result output.Result
	}{
		{"json", output.FormatJSON, nil, brokenResult{}},
		{
			"streaming encoder",
			"yaml",
			[]output.Option{output.WithEncoder("yaml", halfEncoder)},
			listing{Scope: "project", Packages: nil},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			streams, stdout, _ := newStreams()

			printer, err := output.NewPrinter(streams, test.format, test.opts...)
			require.NoError(t, err)

			err = printer.Emit(test.result)
			require.ErrorIs(t, err, errBroken)
			assert.Empty(t, stdout.String())
		})
	}
}

func TestEmitHumanError(t *testing.T) {
	t.Parallel()

	streams, _, _ := newStreams()

	printer, err := output.NewPrinter(streams, output.FormatHuman)
	require.NoError(t, err)
	require.ErrorIs(t, printer.Emit(brokenResult{}), errBroken)
}

func TestNewPrinterRejects(t *testing.T) {
	t.Parallel()

	streams, _, _ := newStreams()

	_, err := output.NewPrinter(streams, "yaml")
	require.ErrorIs(t, err, output.ErrUnknownFormat)

	_, err = output.NewPrinter(streams, "yaml", output.WithEncoder("yaml", nil))
	require.ErrorIs(t, err, output.ErrUnknownFormat)

	_, err = output.NewPrinter(streams, output.FormatHuman,
		output.WithEncoder(output.FormatHuman, fakeYAML))
	require.ErrorIs(t, err, output.ErrUnknownFormat)

	_, err = output.NewPrinter(output.Streams{In: nil, Out: nil, Err: nil}, output.FormatHuman)
	require.ErrorIs(t, err, output.ErrNoStream)
}

func TestPrint(t *testing.T) {
	t.Parallel()

	streams, stdout, _ := newStreams()

	printer, err := output.NewPrinter(streams, output.FormatHuman)
	require.NoError(t, err)

	require.NoError(t, printer.Print("added ", "crud", "\n"))
	require.NoError(t, printer.Printf("installed %d packages\n", 2))
	assert.Equal(t, "added crud\ninstalled 2 packages\n", stdout.String())
}

func TestPrintInMachineFormat(t *testing.T) {
	t.Parallel()

	streams, stdout, _ := newStreams()

	printer, err := output.NewPrinter(streams, output.FormatJSON)
	require.NoError(t, err)

	require.ErrorIs(t, printer.Print("added crud\n"), output.ErrNoMachineForm)
	require.ErrorIs(t, printer.Printf("added %s\n", "crud"), output.ErrNoMachineForm)
	assert.Empty(t, stdout.String())
}

func TestWriterIsStdout(t *testing.T) {
	t.Parallel()

	streams, stdout, _ := newStreams()

	printer, err := output.NewPrinter(streams, output.FormatJSON)
	require.NoError(t, err)

	_, err = io.WriteString(printer.Writer(), "{}\n")
	require.NoError(t, err)
	assert.Equal(t, "{}\n", stdout.String())
}

func TestResolveFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flag     string
		fallback output.Format
		want     output.Format
	}{
		{"empty flag takes the default", "", output.FormatHuman, output.FormatHuman},
		{"empty flag takes a machine default", "", output.FormatJSON, output.FormatJSON},
		{"flag wins over the default", "json", output.FormatHuman, output.FormatJSON},
		{"human spelled table", "table", output.FormatJSON, output.FormatHuman},
		{"registered spelling passes through", "yaml", output.FormatHuman, "yaml"},
		{"unknown value is left to NewPrinter", "xml", output.FormatHuman, "xml"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, output.ResolveFormat(test.flag, test.fallback))
		})
	}
}

func TestTerminal(t *testing.T) {
	t.Parallel()

	streams, stdout, _ := newStreams()

	printer, err := output.NewPrinter(streams, output.FormatHuman)
	require.NoError(t, err)
	assert.False(t, printer.Terminal(), "no probe: not a terminal")

	var probed io.Writer

	probe := func(w io.Writer) bool {
		probed = w

		return true
	}

	printer, err = output.NewPrinter(streams, output.ResolveFormat("", output.FormatHuman),
		output.WithTerminalProbe(probe))
	require.NoError(t, err)
	assert.True(t, printer.Terminal())
	assert.Same(t, stdout, probed, "the probe is asked about stdout")
	assert.Equal(t, output.FormatHuman, printer.Format(), "a terminal does not pick the format")
}

func TestStdStreams(t *testing.T) {
	t.Parallel()

	streams := output.StdStreams()

	assert.Same(t, os.Stdin, streams.In)
	assert.Same(t, os.Stdout, streams.Out)
	assert.Same(t, os.Stderr, streams.Err)
}
