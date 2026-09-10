package registry_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/cli/manifest/registry"
	"github.com/tarantool/tt/cli/manifest/rocks"
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

func TestParseFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		tty  bool
		want registry.Format
	}{
		{name: "a terminal defaults to the table", raw: "", tty: true, want: registry.FormatTable},
		{name: "a pipe defaults to yaml", raw: "", tty: false, want: registry.FormatYAML},
		{name: "explicit table over a pipe", raw: "table", tty: false, want: registry.FormatTable},
		{name: "explicit json", raw: "json", tty: true, want: registry.FormatJSON},
		{name: "explicit yaml", raw: "yaml", tty: true, want: registry.FormatYAML},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			format, err := registry.ParseFormat(testCase.raw, testCase.tty)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, format)
		})
	}
}

func TestParseFormatRejectsUnknown(t *testing.T) {
	t.Parallel()

	_, err := registry.ParseFormat("csv", true)
	require.ErrorIs(t, err, registry.ErrUnknownFormat)
}

// sampleRegistries is the effective list the rendering tests print.
func sampleRegistries() []rocks.Registry {
	return []rocks.Registry{
		{URL: "/srv/mirror", Source: rocks.SourceFlag},
		{URL: "https://rocks.example/", Source: rocks.SourceManifest},
	}
}

func TestRenderListTable(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	require.NoError(t, registry.RenderList(&out, sampleRegistries(), registry.FormatTable))

	lines := splitLines(out.String())
	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], "URL")
	assert.Contains(t, lines[0], "SOURCE")
	// Resolution order is the listing order: the flag entry is queried first.
	assert.Contains(t, lines[1], "/srv/mirror")
	assert.Contains(t, lines[1], "flag")
	assert.Contains(t, lines[2], "https://rocks.example/")
	assert.Contains(t, lines[2], "manifest")
}

func TestRenderListJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	require.NoError(t, registry.RenderList(&out, sampleRegistries(), registry.FormatJSON))

	var decoded []map[string]string

	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	assert.Equal(t, []map[string]string{
		{"url": "/srv/mirror", "source": "flag"},
		{"url": "https://rocks.example/", "source": "manifest"},
	}, decoded)
}

func TestRenderListYAML(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	require.NoError(t, registry.RenderList(&out, sampleRegistries(), registry.FormatYAML))

	var decoded []map[string]string

	require.NoError(t, yaml.Unmarshal(out.Bytes(), &decoded))
	assert.Equal(t, []map[string]string{
		{"url": "/srv/mirror", "source": "flag"},
		{"url": "https://rocks.example/", "source": "manifest"},
	}, decoded)
}

func TestRenderSearchTable(t *testing.T) {
	t.Parallel()

	var out, notes bytes.Buffer

	matches := []registry.Match{
		{Name: "stat", Version: "0.3.2-1", Server: "https://rocks.example/"},
		{Name: "stat", Version: "0.3.1-1", Server: "https://rocks.example/"},
	}

	require.NoError(t, registry.RenderSearch(&out, &notes, matches, "stat", registry.FormatTable))

	lines := splitLines(out.String())
	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "VERSION")
	assert.Contains(t, lines[0], "SERVER")
	assert.Contains(t, lines[1], "0.3.2-1")
	assert.Empty(t, notes.String())
}

func TestRenderSearchNoMatchKeepsStdoutClean(t *testing.T) {
	t.Parallel()

	var out, notes bytes.Buffer

	err := registry.RenderSearch(&out, &notes, nil, "unpublished", registry.FormatTable)
	require.NoError(t, err)

	// A miss is an answer, not an error: nothing goes to the stream a caller
	// may be piping, and the narrative says so on the other one.
	assert.Empty(t, out.String())
	assert.Contains(t, notes.String(), "unpublished")
}

func TestRenderSearchNoMatchIsAnEmptyDocument(t *testing.T) {
	t.Parallel()

	for _, format := range []registry.Format{registry.FormatJSON, registry.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()

			var out, notes bytes.Buffer

			require.NoError(t, registry.RenderSearch(&out, &notes, nil, "unpublished", format))

			// A consumer parsing the output must get a valid empty list, not
			// an empty file.
			var decoded []registry.Match

			require.NoError(t, yaml.Unmarshal(out.Bytes(), &decoded))
			assert.Empty(t, decoded)
		})
	}
}

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
