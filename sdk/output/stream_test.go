package output_test

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/output"
)

// recorder is a stdout that remembers every write and flush separately, so
// a test sees when each item reached it.
type recorder struct {
	mu      sync.Mutex
	writes  []string
	flushes int
	fail    error
}

func (r *recorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fail != nil {
		return 0, r.fail
	}

	r.writes = append(r.writes, string(data))

	return len(data), nil
}

func (r *recorder) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.flushes++

	return nil
}

func (r *recorder) String() string {
	writes, _ := r.snapshot()

	return strings.Join(writes, "")
}

func (r *recorder) snapshot() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.writes...), r.flushes
}

// record is a stream item: one line for people, an object for machines.
type record struct {
	LSN   int `json:"lsn"`
	Tuple any `json:"tuple"`
}

func (r record) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%d\t%v\n", r.LSN, r.Tuple)

	return err
}

// halfRecord renders part of an item and then fails, in every format.
type halfRecord struct{}

func (halfRecord) Human(w io.Writer) error {
	_, _ = io.WriteString(w, "half")

	return errBroken
}

// linesEncoder is a fake stream encoder with a header, a line per item and
// a trailer, standing in for one the core registers.
type linesEncoder struct {
	w io.Writer
}

func newLines(w io.Writer) output.StreamEncoder {
	_, _ = io.WriteString(w, "# begin\n")

	return linesEncoder{w: w}
}

func (l linesEncoder) Encode(item any) error {
	if _, ok := item.(halfRecord); ok {
		_, _ = io.WriteString(l.w, "line: hal")

		return errBroken
	}

	_, err := fmt.Fprintf(l.w, "line: %+v\n", item)

	return err
}

func (l linesEncoder) Close() error {
	_, err := io.WriteString(l.w, "# end\n")

	return err
}

// failingClose is a stream encoder whose Close fails after writing part of
// a trailer.
type failingClose struct {
	linesEncoder
}

func (f failingClose) Close() error {
	_, _ = io.WriteString(f.w, "# en")

	return errBroken
}

func newRecorderPrinter(
	t *testing.T, format output.Format, opts ...output.Option,
) (*output.Printer, *recorder) {
	t.Helper()

	out := &recorder{mu: sync.Mutex{}, writes: nil, flushes: 0, fail: nil}

	printer, err := output.NewPrinter(output.Streams{In: nil, Out: out, Err: io.Discard},
		format, opts...)
	require.NoError(t, err)

	return printer, out
}

func TestStream(t *testing.T) {
	t.Parallel()

	items := []output.Result{
		record{LSN: 1, Tuple: []any{int8(1), "a"}},
		record{LSN: 2, Tuple: map[any]any{int8(1): "x"}},
		listing{Scope: "project", Packages: []string{"crud"}},
	}

	tests := []struct {
		name   string
		format output.Format
		opts   []output.Option
		// open is written when the stream opens, close when it closes.
		open  string
		want  []string
		close string
	}{
		{
			name:   "json lines",
			format: output.FormatJSON,
			opts:   nil,
			open:   "",
			want: []string{
				`{"lsn":1,"tuple":[1,"a"]}` + "\n",
				`{"lsn":2,"tuple":{"1":"x"}}` + "\n",
				`{"scope":"project","packages":["crud"]}` + "\n",
			},
			close: "",
		},
		{
			name:   "human",
			format: output.FormatHuman,
			opts:   nil,
			open:   "",
			want:   []string{"1\t[1 a]\n", "2\tmap[1:x]\n", "crud\n"},
			close:  "",
		},
		{
			name:   "registered stream encoder",
			format: "lines",
			opts: []output.Option{
				output.WithEncoder("lines", fakeYAML),
				output.WithStreamEncoder("lines", newLines),
			},
			open: "# begin\n",
			want: []string{
				"line: {LSN:1 Tuple:[1 a]}\n",
				"line: {LSN:2 Tuple:map[1:x]}\n",
				"line: {Scope:project Packages:[crud]}\n",
			},
			close: "# end\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			printer, out := newRecorderPrinter(t, test.format, test.opts...)

			stream, err := printer.Stream()
			require.NoError(t, err)

			var wantWrites []string

			if test.open != "" {
				wantWrites = append(wantWrites, test.open)
			}

			writes, _ := out.snapshot()
			require.Equal(t, wantWrites, writes, "what the stream writes on opening")

			for index, item := range items {
				require.NoError(t, stream.Emit(item))

				wantWrites = append(wantWrites, test.want[index])

				writes, flushes := out.snapshot()
				require.Equal(t, wantWrites, writes,
					"item %d is written as one write when emitted", index)
				require.Equal(t, len(wantWrites), flushes,
					"stdout is flushed after item %d", index)
			}

			require.NoError(t, stream.Close())

			if test.close != "" {
				wantWrites = append(wantWrites, test.close)
			}

			writes, _ = out.snapshot()
			assert.Equal(t, wantWrites, writes)
		})
	}
}

func TestStreamJSONLinesBytes(t *testing.T) {
	t.Parallel()

	streams, stdout, _ := newStreams()

	printer, err := output.NewPrinter(streams, output.FormatJSON)
	require.NoError(t, err)

	stream, err := printer.Stream()
	require.NoError(t, err)
	require.NoError(t, stream.Emit(record{LSN: 7, Tuple: []any{uint16(300), []byte("\x00"), nil}}))
	require.NoError(t, stream.Emit(record{LSN: 8, Tuple: map[any]any{true: "<b>"}}))
	require.NoError(t, stream.Emit(record{LSN: 9, Tuple: map[any]any{
		"r":          []any{math.NaN(), float32(math.Inf(1)), 0.5},
		math.Inf(-1): float32(math.NaN()),
	}}))
	require.NoError(t, stream.Close())

	// "<b>" is HTML-escaped as in the one-shot JSON: backslash-u escapes.
	assert.Equal(t, //nolint:testifylint // Byte-exact JSON lines, not one JSON value.
		`{"lsn":7,"tuple":[300,"AA==",null]}`+"\n"+
			`{"lsn":8,"tuple":{"true":"`+"\x5cu003cb\x5cu003e"+`"}}`+"\n"+
			`{"lsn":9,"tuple":{"-Infinity":"NaN","r":["NaN","Infinity",0.5]}}`+"\n",
		stdout.String())

	for line := range strings.Lines(stdout.String()) {
		assert.True(t, json.Valid([]byte(line)), "every line is a JSON value: %s", line)
	}
}

func TestStreamErrorMidway(t *testing.T) {
	t.Parallel()

	first := record{LSN: 1, Tuple: "a"}
	last := record{LSN: 3, Tuple: "c"}

	tests := []struct {
		name   string
		format output.Format
		opts   []output.Option
		broken output.Result
		want   string
	}{
		{
			name:   "json encoding fails",
			format: output.FormatJSON,
			opts:   nil,
			broken: brokenResult{},
			want:   `{"lsn":1,"tuple":"a"}` + "\n" + `{"lsn":3,"tuple":"c"}` + "\n",
		},
		{
			name:   "json key collision",
			format: output.FormatJSON,
			opts:   nil,
			broken: record{LSN: 2, Tuple: map[any]any{int8(1): "a", "1": "b"}},
			want:   `{"lsn":1,"tuple":"a"}` + "\n" + `{"lsn":3,"tuple":"c"}` + "\n",
		},
		{
			name:   "human rendering fails halfway",
			format: output.FormatHuman,
			opts:   nil,
			broken: halfRecord{},
			want:   "1\ta\n3\tc\n",
		},
		{
			name:   "stream encoder fails halfway",
			format: "lines",
			opts: []output.Option{
				output.WithEncoder("lines", fakeYAML),
				output.WithStreamEncoder("lines", newLines),
			},
			broken: halfRecord{},
			want: "# begin\nline: {LSN:1 Tuple:a}\nline: {LSN:3 Tuple:c}\n" +
				"# end\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			printer, out := newRecorderPrinter(t, test.format, test.opts...)

			stream, err := printer.Stream()
			require.NoError(t, err)
			require.NoError(t, stream.Emit(first))

			before := out.String()

			require.Error(t, stream.Emit(test.broken))
			assert.Equal(t, before, out.String(),
				"a failed item leaves the earlier ones and adds nothing")

			require.NoError(t, stream.Emit(last), "the stream stays open after a failed item")
			require.NoError(t, stream.Close())
			assert.Equal(t, test.want, out.String())
		})
	}
}

func TestStreamWriteError(t *testing.T) {
	t.Parallel()

	printer, out := newRecorderPrinter(t, output.FormatJSON)

	stream, err := printer.Stream()
	require.NoError(t, err)
	require.NoError(t, stream.Emit(record{LSN: 1, Tuple: "a"}))

	out.mu.Lock()

	out.fail = errBroken
	out.mu.Unlock()

	err = stream.Emit(record{LSN: 2, Tuple: "b"})
	require.ErrorIs(t, err, errBroken)
	require.ErrorContains(t, err, "writing output")
	require.NoError(t, stream.Close())
	assert.JSONEq(t, `{"lsn":1,"tuple":"a"}`+"\n", out.String())
}

func TestStreamCloseError(t *testing.T) {
	t.Parallel()

	printer, out := newRecorderPrinter(t, "lines",
		output.WithEncoder("lines", fakeYAML),
		output.WithStreamEncoder("lines", func(w io.Writer) output.StreamEncoder {
			return failingClose{linesEncoder{w: w}}
		}))

	stream, err := printer.Stream()
	require.NoError(t, err)
	require.ErrorIs(t, stream.Close(), errBroken)
	assert.Empty(t, out.String(), "a failed trailer is not written")

	_, err = printer.Stream()
	require.NoError(t, err, "a failed Close still releases the printer")
}

func TestStreamNotStreamable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format output.Format
		opts   []output.Option
	}{
		{
			name:   "registered format without a stream encoder",
			format: "yaml",
			opts:   []output.Option{output.WithEncoder("yaml", fakeYAML)},
		},
		{
			name:   "stream encoder removed",
			format: output.FormatJSON,
			opts:   []output.Option{output.WithStreamEncoder(output.FormatJSON, nil)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			printer, out := newRecorderPrinter(t, test.format, test.opts...)

			_, err := printer.Stream()
			require.ErrorIs(t, err, output.ErrNoStreamForm)
			assert.Empty(t, out.String())

			require.NoError(t, printer.Emit(listing{Scope: "project", Packages: nil}),
				"the refused stream leaves the printer usable")
		})
	}
}

func TestStreamExclusive(t *testing.T) {
	t.Parallel()

	result := listing{Scope: "project", Packages: []string{"crud"}}

	printer, out := newRecorderPrinter(t, output.FormatHuman)

	stream, err := printer.Stream()
	require.NoError(t, err)
	require.NoError(t, stream.Emit(record{LSN: 1, Tuple: "a"}))

	require.ErrorIs(t, printer.Print("text\n"), output.ErrStreamOpen)
	require.ErrorIs(t, printer.Printf("%s\n", "text"), output.ErrStreamOpen)
	require.ErrorIs(t, printer.Emit(result), output.ErrStreamOpen)

	_, err = printer.Stream()
	require.ErrorIs(t, err, output.ErrStreamOpen)

	require.NoError(t, stream.Close())
	require.NoError(t, stream.Close(), "closing twice does nothing")
	require.ErrorIs(t, stream.Emit(record{LSN: 2, Tuple: "b"}), output.ErrStreamClosed)

	require.NoError(t, printer.Print("text\n"))
	require.NoError(t, printer.Emit(result))

	again, err := printer.Stream()
	require.NoError(t, err, "a printer streams again once the stream is closed")
	require.NoError(t, again.Close())

	assert.Equal(t, "1\ta\ntext\ncrud\n", out.String())
}

func TestStreamConcurrentEmit(t *testing.T) {
	t.Parallel()

	const (
		producers = 8
		perEach   = 50
	)

	printer, out := newRecorderPrinter(t, output.FormatJSON)

	stream, err := printer.Stream()
	require.NoError(t, err)

	start := make(chan struct{})

	var group sync.WaitGroup

	for p := range producers {
		group.Go(func() {
			<-start

			for i := range perEach {
				item := record{LSN: p*perEach + i, Tuple: strings.Repeat("x", 64)}
				assert.NoError(t, stream.Emit(item))
			}
		})
	}

	close(start)
	group.Wait()
	require.NoError(t, stream.Close())

	writes, _ := out.snapshot()
	require.Len(t, writes, producers*perEach)

	seen := map[int]bool{}

	for _, line := range writes {
		var got record

		require.NoError(t, json.Unmarshal([]byte(line), &got), "an intact line: %q", line)

		seen[got.LSN] = true
	}

	assert.Len(t, seen, producers*perEach)
}

func TestNewPrinterRejectsStreamEncoders(t *testing.T) {
	t.Parallel()

	streams, _, _ := newStreams()

	_, err := output.NewPrinter(streams, output.FormatHuman,
		output.WithStreamEncoder(output.FormatHuman, newLines))
	require.ErrorIs(t, err, output.ErrUnknownFormat)

	_, err = output.NewPrinter(streams, output.FormatHuman,
		output.WithStreamEncoder("lines", newLines))
	require.ErrorIs(t, err, output.ErrUnknownFormat)
	require.ErrorContains(t, err, `a stream encoder for "lines", which has no encoder`)
}
