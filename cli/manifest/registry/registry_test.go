package registry_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"

	"github.com/tarantool/tt/v3/cli/manifest/registry"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/printing"
)

func TestParseRef(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		arg  string
		want registry.Ref
	}{
		{name: "bare name", arg: "metrics", want: registry.Ref{Name: "metrics"}},
		{
			name: "name and version",
			arg:  "metrics@1.0.0-1",
			want: registry.Ref{Name: "metrics", Version: "1.0.0-1"},
		},
		{
			name: "a namespaced name survives",
			arg:  "tarantool/metrics@1.0.0-1",
			want: registry.Ref{Name: "tarantool/metrics", Version: "1.0.0-1"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ref, err := registry.ParseRef(testCase.arg)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, ref)
			// The rendering is what an error message and a re-run show, so it
			// has to be the argument that was typed.
			assert.Equal(t, testCase.arg, ref.String())
		})
	}
}

func TestParseRefRejects(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"", "@1.0.0-1", "metrics@", "metrics@   "} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()

			_, err := registry.ParseRef(arg)
			require.ErrorIs(t, err, registry.ErrBadReference)
		})
	}
}

// sampleRegistries is the effective list the rendering tests print.
func sampleRegistries() registry.Servers {
	return registry.Servers{
		{URL: "/srv/mirror", Source: rocks.SourceFlag},
		{URL: "https://rocks.example/", Source: rocks.SourceManifest},
	}
}

// emit writes result through a Printer in format, the way the commands do,
// and returns stdout.
func emit(t *testing.T, result output.Result, format output.Format) []byte {
	t.Helper()

	var out bytes.Buffer

	printer, err := output.NewPrinter(output.Streams{In: nil, Out: &out, Err: io.Discard},
		format, printing.Options()...)
	require.NoError(t, err)
	require.NoError(t, printer.Emit(result))

	return out.Bytes()
}

func TestServersTable(t *testing.T) {
	t.Parallel()

	lines := splitLines(string(emit(t, sampleRegistries(), output.FormatHuman)))
	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], "URL")
	assert.Contains(t, lines[0], "SOURCE")
	// Resolution order is the listing order: the flag entry is queried first.
	assert.Contains(t, lines[1], "/srv/mirror")
	assert.Contains(t, lines[1], "flag")
	assert.Contains(t, lines[2], "https://rocks.example/")
	assert.Contains(t, lines[2], "manifest")
}

func TestServersJSON(t *testing.T) {
	t.Parallel()

	var decoded []map[string]string

	require.NoError(t, json.Unmarshal(emit(t, sampleRegistries(), output.FormatJSON), &decoded))
	assert.Equal(t, []map[string]string{
		{"url": "/srv/mirror", "source": "flag"},
		{"url": "https://rocks.example/", "source": "manifest"},
	}, decoded)
}

func TestServersYAML(t *testing.T) {
	t.Parallel()

	var decoded []map[string]string

	require.NoError(t, yaml.Unmarshal(emit(t, sampleRegistries(), printing.FormatYAML), &decoded))
	assert.Equal(t, []map[string]string{
		{"url": "/srv/mirror", "source": "flag"},
		{"url": "https://rocks.example/", "source": "manifest"},
	}, decoded)
}

// sampleMatches is a search that found two versions of one rock.
func sampleMatches() []registry.Match {
	return []registry.Match{
		{Name: "stat", Version: "0.3.2-1", Server: "https://rocks.example/"},
		{Name: "stat", Version: "0.3.1-1", Server: "https://rocks.example/"},
	}
}

func TestSearchTable(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	var out bytes.Buffer

	result := registry.SearchResult{Term: "stat", Matches: sampleMatches()}
	require.NoError(t, result.HumanTo(&out, logger))

	lines := splitLines(out.String())
	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "VERSION")
	assert.Contains(t, lines[0], "SERVER")
	assert.Contains(t, lines[1], "0.3.2-1")
	assert.Empty(t, recorder.Records())
}

func TestSearchNoMatchKeepsStdoutClean(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	var out bytes.Buffer

	result := registry.SearchResult{Term: "unpublished", Matches: []registry.Match{}}
	require.NoError(t, result.HumanTo(&out, logger))

	// A miss is an answer, not an error: nothing goes to the stream a caller
	// may be piping, and a log line says so on the other one.
	assert.Empty(t, out.String())

	records := recorder.Records()
	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelInfo, records[0].Level)
	assert.Equal(t, `no rock matches "unpublished"`, records[0].Message)
}

// TestSearchMachineFormsAreTheMatchList pins that the machine formats carry
// the matches exactly as a plain list of them encodes - the term is not part
// of the document.
func TestSearchMachineFormsAreTheMatchList(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.FormatJSON, printing.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()

			for _, matches := range [][]registry.Match{sampleMatches(), {}, nil} {
				result := registry.SearchResult{Term: "stat", Matches: matches}

				assert.Equal(t, string(emit(t, plainList(matches), format)),
					string(emit(t, result, format)))
			}
		})
	}
}

func TestSearchNoMatchIsAnEmptyDocument(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.FormatJSON, printing.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()

			result := registry.SearchResult{Term: "unpublished", Matches: []registry.Match{}}

			// A consumer parsing the output must get a valid empty list, not
			// an empty file.
			assert.Equal(t, "[]\n", string(emit(t, result, format)))
		})
	}
}

// plainList is a list of matches with no encoding of its own.
type plainList []registry.Match

func (plainList) Human(io.Writer) error { return nil }

// splitLines splits rendered output into non-empty lines.
func splitLines(text string) []string {
	var lines []string

	for line := range bytes.Lines([]byte(text)) {
		trimmed := bytes.TrimRight(line, "\n")
		if len(trimmed) > 0 {
			lines = append(lines, string(trimmed))
		}
	}

	return lines
}
