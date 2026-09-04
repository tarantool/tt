package inventory_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/cli/manifest/inventory"
	"github.com/tarantool/tt/cli/manifest/state"
)

// render writes a listing in the given format and returns it as a string.
func render(t *testing.T, listing *inventory.Listing, format inventory.Format) string {
	t.Helper()

	var out strings.Builder

	require.NoError(t, inventory.Render(&out, listing, format))

	return out.String()
}

// TestParseFormatExplicit covers the three accepted values.
func TestParseFormatExplicit(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]inventory.Format{
		"table": inventory.FormatTable,
		"json":  inventory.FormatJSON,
		"yaml":  inventory.FormatYAML,
	} {
		for _, tty := range []bool{true, false} {
			got, err := inventory.ParseFormat(raw, tty)
			require.NoError(t, err, raw)
			assert.Equal(t, want, got, raw)
		}
	}
}

// TestParseFormatDefaultsByTTY pins the convention: a terminal gets the human
// table, a pipe gets YAML so piped output is parseable without a flag.
func TestParseFormatDefaultsByTTY(t *testing.T) {
	t.Parallel()

	got, err := inventory.ParseFormat("", true)
	require.NoError(t, err)
	assert.Equal(t, inventory.FormatTable, got)

	got, err = inventory.ParseFormat("", false)
	require.NoError(t, err)
	assert.Equal(t, inventory.FormatYAML, got)
}

// TestParseFormatRejectsUnknown pins that a mistyped -o is exit 1, not a silent
// fallback to the table.
func TestParseFormatRejectsUnknown(t *testing.T) {
	t.Parallel()

	_, err := inventory.ParseFormat("xml", true)
	require.Error(t, err)
	require.ErrorIs(t, err, inventory.ErrUnknownFormat)
	assert.Equal(t, 1, inventory.ExitCode(err))
	assert.Contains(t, err.Error(), "xml")
}

// TestRenderYAMLIsValid pins that the YAML output round-trips.
func TestRenderYAMLIsValid(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installPrimary(t, pkg{name: "my-app", version: "1.2.3"})
	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"}})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	var decoded inventory.Listing

	require.NoError(t, yaml.Unmarshal([]byte(render(t, listing, inventory.FormatYAML)), &decoded))

	assert.Equal(t, state.ScopeProject, decoded.Scope)
	require.Len(t, decoded.Packages, 2)
	assert.Equal(t, "my-app", decoded.Packages[0].Name)
	assert.True(t, decoded.Packages[0].Primary)
	assert.Equal(t, map[string]string{"metrics": "1.0.0"}, decoded.Packages[1].Dependencies)
}

// TestRenderTable pins the human output: a header, one row per package, and the
// primary package marked so it is obvious which one uninstall will refuse.
func TestRenderTable(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installPrimary(t, pkg{name: "my-app", version: "1.2.3", description: "The application"})
	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0"})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	out := render(t, listing, inventory.FormatTable)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "VERSION")
	assert.Contains(t, lines[0], "ORIGIN")

	assert.Contains(t, lines[1], "my-app")
	assert.Contains(t, lines[1], "1.2.3")
	assert.Contains(t, lines[1], "primary")
	assert.Contains(t, lines[1], "The application")

	assert.Contains(t, lines[2], "monitoring")
	assert.Contains(t, lines[2], "guest")
}

// TestRenderTableEmpty pins that an empty scope says so in a sentence rather
// than printing a lone header row, which reads like a bug.
func TestRenderTableEmpty(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	out := render(t, listing, inventory.FormatTable)

	assert.Contains(t, out, "no packages installed")
	assert.NotContains(t, out, "NAME")
}

// TestRenderTableMissingVersion pins that an empty column renders as "-" rather
// than a gap that looks like truncation.
func TestRenderTableMissingVersion(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	// A project with a manifest but no VERSION pin next to it.
	tr.installPrimary(t, pkg{name: "my-app", version: ""})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	out := render(t, listing, inventory.FormatTable)

	assert.Contains(t, out, "my-app")
	assert.Contains(t, out, "-")
}

// TestRenderTableFlattensDescription pins that a multi-line description cannot
// break the column alignment.
func TestRenderTableFlattensDescription(t *testing.T) {
	t.Parallel()

	listing := &inventory.Listing{
		Scope: state.ScopeProject,
		Root:  "/tmp/project",
		Packages: []inventory.Entry{{
			Name: "my-app", Version: "1.0.0", Primary: false,
			Description:  "first line\nsecond line",
			Dependencies: nil,
		}},
	}

	out := render(t, listing, inventory.FormatTable)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	require.Len(t, lines, 2, "a multi-line description must not add rows")
	assert.Contains(t, lines[1], "first line second line")
}

// TestRenderRejectsUnknownFormat pins that an unvalidated format cannot fall
// through to a silent default.
func TestRenderRejectsUnknownFormat(t *testing.T) {
	t.Parallel()

	listing := &inventory.Listing{
		Scope: state.ScopeProject, Root: "/tmp/project", Packages: nil,
	}

	var out strings.Builder

	err := inventory.Render(&out, listing, inventory.Format("xml"))
	require.Error(t, err)
	require.ErrorIs(t, err, inventory.ErrUnknownFormat)
}
