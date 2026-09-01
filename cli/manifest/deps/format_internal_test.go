package deps

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// sampleReport is one report covering every shape the renderers have to carry:
// a direct dependency, an indirect one, a dev dependency, and a stale lock.
func sampleReport() *Report {
	return &Report{
		Package:    "my-app",
		Lock:       LockStale,
		LockReason: "manifest changed since the lock was written",
		Products: []ProductEntries{{
			Name: "default",
			Dependencies: []Entry{
				{
					Name: "checks", Constraint: ">=3.0.0", Version: "3.1.0-1",
					Source: "registry", Direct: true, DeclaredIn: []string{"[dependencies]"},
				},
				{Name: "luasocket", Version: "3.0.0-1", Source: "registry"},
			},
		}},
		DevDependencies: []Entry{{
			Name: "luatest", Constraint: "*", Version: "1.0.1-1",
			Source: "registry", Direct: true, DeclaredIn: []string{"[dev_dependencies]"},
		}},
	}
}

// TestParseFormat_defaultFollowsStdout: a terminal gets the table, anything
// else gets YAML, so piped output is parseable without a flag.
func TestParseFormat_defaultFollowsStdout(t *testing.T) {
	t.Parallel()

	tty, err := ParseFormat("", true)
	require.NoError(t, err)
	assert.Equal(t, FormatTable, tty)

	piped, err := ParseFormat("", false)
	require.NoError(t, err)
	assert.Equal(t, FormatYAML, piped)
}

// TestParseFormat_unknownIsRefused: a mistyped -o must not silently fall back
// to a format the caller did not ask for.
func TestParseFormat_unknownIsRefused(t *testing.T) {
	t.Parallel()

	_, err := ParseFormat("xml", true)
	require.ErrorIs(t, err, ErrUnknownFormat)
	assert.Equal(t, exitStateError, ExitCode(err))
}

// TestRender_jsonIsValidAndCarriesEveryGroup is the acceptance criterion for
// -o json: something else parses it, and finds both closures in it.
func TestRender_jsonIsValidAndCarriesEveryGroup(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, Render(&out, sampleReport(), FormatJSON))

	var decoded struct {
		Package  string `json:"package"`
		Lock     string `json:"lock"`
		Products []struct {
			Name         string `json:"name"`
			Dependencies []struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Direct  bool   `json:"direct"`
			} `json:"dependencies"`
		} `json:"products"`
		DevDependencies []struct {
			Name string `json:"name"`
		} `json:"dev_dependencies"`
	}

	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))

	assert.Equal(t, "my-app", decoded.Package)
	assert.Equal(t, "stale", decoded.Lock)
	require.Len(t, decoded.Products, 1)
	require.Len(t, decoded.Products[0].Dependencies, 2)
	assert.True(t, decoded.Products[0].Dependencies[0].Direct)
	assert.False(t, decoded.Products[0].Dependencies[1].Direct)
	require.Len(t, decoded.DevDependencies, 1)
	assert.Equal(t, "luatest", decoded.DevDependencies[0].Name)
}

// TestRender_yamlIsValid covers the format a piped run gets by default.
func TestRender_yamlIsValid(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, Render(&out, sampleReport(), FormatYAML))

	var decoded map[string]any
	require.NoError(t, yaml.Unmarshal(out.Bytes(), &decoded))

	assert.Equal(t, "my-app", decoded["package"])
	assert.Contains(t, decoded, "dev_dependencies")
}

// TestRender_tableStatesTheLockAndEveryRow: the human view has to say what the
// versions are worth before showing them, and put the dev closure somewhere a
// reader can tell apart from a product.
func TestRender_tableStatesTheLockAndEveryRow(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, Render(&out, sampleReport(), FormatTable))

	text := out.String()
	assert.Contains(t, text, "lock is stale")
	assert.Contains(t, text, "tt package resolve")
	assert.Contains(t, text, "SCOPE")

	lines := strings.Split(strings.TrimSpace(text), "\n")
	joined := strings.Join(lines, "\n")

	assert.Regexp(t, `default\s+checks\s+>=3\.0\.0\s+3\.1\.0-1\s+registry\s+direct`, joined)
	assert.Regexp(t, `default\s+luasocket\s+-\s+3\.0\.0-1\s+registry\s+indirect`, joined)
	assert.Regexp(t, `\(dev\)\s+luatest\s+\*\s+1\.0\.1-1\s+registry\s+direct`, joined)
}

// TestRender_tableSaysSoWhenNothingIsDeclared: a bare header over an empty
// table reads as a bug, so the empty case gets a sentence.
func TestRender_tableSaysSoWhenNothingIsDeclared(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	report := &Report{
		Package:  "my-app",
		Lock:     LockCurrent,
		Products: []ProductEntries{{Name: "default"}},
	}
	require.NoError(t, Render(&out, report, FormatTable))

	assert.Equal(t, "my-app declares no dependencies\n", out.String())
}

// TestRender_tablePointsAMissingLockAtResolve: the answer to "why is the
// VERSION column empty" belongs in the output, not in the documentation.
func TestRender_tablePointsAMissingLockAtResolve(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	report := sampleReport()

	report.Lock = LockMissing
	report.LockReason = ""

	require.NoError(t, Render(&out, report, FormatTable))
	assert.Contains(t, out.String(), "no lock yet: run tt package resolve")
}

// TestRender_unknownFormatIsRefused guards the switch's default arm: a format
// that reached Render unchecked must fail rather than print nothing.
func TestRender_unknownFormatIsRefused(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	err := Render(&out, sampleReport(), Format("xml"))
	require.ErrorIs(t, err, ErrUnknownFormat)
	assert.Empty(t, out.String())
}
