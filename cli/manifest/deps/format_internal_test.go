package deps

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"

	"github.com/tarantool/tt/v3/cli/printing"
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

// emit writes the report through a Printer in format, the way tt package deps
// does, and returns stdout.
func emit(t *testing.T, report *Report, format output.Format) []byte {
	t.Helper()

	var out bytes.Buffer

	printer, err := output.NewPrinter(output.Streams{In: nil, Out: &out, Err: io.Discard},
		format, printing.Options()...)
	require.NoError(t, err)
	require.NoError(t, printer.Emit(report))

	return out.Bytes()
}

// TestReport_jsonIsValidAndCarriesEveryGroup is the acceptance criterion for
// -o json: something else parses it, and finds both closures in it.
func TestReport_jsonIsValidAndCarriesEveryGroup(t *testing.T) {
	t.Parallel()

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

	require.NoError(t, json.Unmarshal(emit(t, sampleReport(), output.FormatJSON), &decoded))

	assert.Equal(t, "my-app", decoded.Package)
	assert.Equal(t, "stale", decoded.Lock)
	require.Len(t, decoded.Products, 1)
	require.Len(t, decoded.Products[0].Dependencies, 2)
	assert.True(t, decoded.Products[0].Dependencies[0].Direct)
	assert.False(t, decoded.Products[0].Dependencies[1].Direct)
	require.Len(t, decoded.DevDependencies, 1)
	assert.Equal(t, "luatest", decoded.DevDependencies[0].Name)
}

// TestReport_yamlIsValid covers the other machine format.
func TestReport_yamlIsValid(t *testing.T) {
	t.Parallel()

	var decoded map[string]any

	require.NoError(t, yaml.Unmarshal(emit(t, sampleReport(), printing.FormatYAML), &decoded))

	assert.Equal(t, "my-app", decoded["package"])
	assert.Equal(t, "stale", decoded["lock"])
	assert.Contains(t, decoded, "dev_dependencies")
}

// TestReport_tableRowsAndTheLockNoteApart: the human view puts the dev
// closure somewhere a reader can tell apart from a product, and says what the
// versions are worth in a log line rather than in the table, so a redirected
// table keeps every row and nothing else.
func TestReport_tableRowsAndTheLockNoteApart(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	var out bytes.Buffer

	require.NoError(t, sampleReport().human(&out, logger))

	records := recorder.Records()
	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelWarn, records[0].Level)
	assert.Contains(t, records[0].Message, "lock is stale")
	assert.Contains(t, records[0].Message, "manifest changed since the lock was written")
	assert.Contains(t, records[0].Message, "tt package resolve")

	text := out.String()
	assert.NotContains(t, text, "lock is stale")
	assert.True(t, strings.HasPrefix(text, "PRODUCT"), text)

	assert.Regexp(t, `default\s+checks\s+>=3\.0\.0\s+3\.1\.0-1\s+registry\s+direct`, text)
	assert.Regexp(t, `default\s+luasocket\s+-\s+3\.0\.0-1\s+registry\s+transitive`, text)
	assert.Regexp(t, `\(dev\)\s+luatest\s+\*\s+1\.0\.1-1\s+registry\s+direct`, text)
}

// TestReport_tableSaysSoWhenNothingIsDeclared: a bare header over an empty
// table reads as a bug, so the empty case gets a sentence. A current lock
// needs no note.
func TestReport_tableSaysSoWhenNothingIsDeclared(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	var out bytes.Buffer

	report := &Report{
		Package:  "my-app",
		Lock:     LockCurrent,
		Products: []ProductEntries{{Name: "default"}},
	}
	require.NoError(t, report.human(&out, logger))

	assert.Equal(t, "my-app declares no dependencies\n", out.String())
	assert.Empty(t, recorder.Records())
}

// TestReport_tablePointsAMissingLockAtResolve: the answer to "why is the
// VERSION column empty" belongs in the output, not in the documentation.
func TestReport_tablePointsAMissingLockAtResolve(t *testing.T) {
	t.Parallel()

	logger, recorder := logtest.New(t)

	var out bytes.Buffer

	report := sampleReport()

	report.Lock = LockMissing
	report.LockReason = ""

	require.NoError(t, report.human(&out, logger))

	records := recorder.Records()
	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelInfo, records[0].Level)
	assert.Equal(t, "no lock yet: run tt package resolve to pin versions", records[0].Message)
	assert.NotContains(t, out.String(), "no lock yet")
}
