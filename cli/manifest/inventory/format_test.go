package inventory_test

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/output"

	"github.com/tarantool/tt/v3/cli/manifest/inventory"
	"github.com/tarantool/tt/v3/cli/manifest/state"
	"github.com/tarantool/tt/v3/cli/printing"
)

// render writes a listing through a Printer in the given format, the way
// tt package list does, and returns stdout as a string.
func render(t *testing.T, listing *inventory.Listing, format output.Format) string {
	t.Helper()

	var out strings.Builder

	printer, err := output.NewPrinter(output.Streams{In: nil, Out: &out, Err: io.Discard},
		format, printing.Options()...)
	require.NoError(t, err)
	require.NoError(t, printer.Emit(listing))

	return out.String()
}

// TestRenderYAMLIsValid pins that the YAML output round-trips.
func TestRenderYAMLIsValid(t *testing.T) {
	t.Parallel()

	fixture := newTree(t)

	fixture.installPrimary(t, pkg{name: "my-app", version: "1.2.3"})
	fixture.installGuest(t, pkg{name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"}})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: fixture.dir})
	require.NoError(t, err)

	var decoded inventory.Listing

	require.NoError(t, yaml.Unmarshal([]byte(render(t, listing, printing.FormatYAML)), &decoded))

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

	fixture := newTree(t)

	fixture.installPrimary(t, pkg{name: "my-app", version: "1.2.3", description: "The application"})
	fixture.installGuest(t, pkg{name: "monitoring", version: "2.0.0"})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: fixture.dir})
	require.NoError(t, err)

	out := render(t, listing, output.FormatHuman)
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

	fixture := newTree(t)

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: fixture.dir})
	require.NoError(t, err)

	out := render(t, listing, output.FormatHuman)

	assert.Contains(t, out, "no packages installed")
	assert.NotContains(t, out, "NAME")
}

// TestRenderTableMissingVersion pins that an empty column renders as "-" rather
// than a gap that looks like truncation.
func TestRenderTableMissingVersion(t *testing.T) {
	t.Parallel()

	fixture := newTree(t)

	// A project with a manifest but no VERSION pin next to it.
	fixture.installPrimary(t, pkg{name: "my-app", version: ""})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: fixture.dir})
	require.NoError(t, err)

	out := render(t, listing, output.FormatHuman)

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

	out := render(t, listing, output.FormatHuman)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	require.Len(t, lines, 2, "a multi-line description must not add rows")
	assert.Contains(t, lines[1], "first line second line")
}
