package inventory_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/inventory"
	"github.com/tarantool/tt/cli/manifest/state"
)

// TestListPrimaryAndGuests is the task's headline case: a project holding its
// own package and two guests lists all three.
func TestListPrimaryAndGuests(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installPrimary(t, pkg{
		name: "my-app", version: "1.2.3", description: "The application",
		deps: map[string]string{"metrics": ">=1.0.0"},
		pins: map[string]string{"metrics": "1.0.0"},
	})
	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0", description: "Monitoring guest",
		pins: map[string]string{"metrics": "1.0.0"},
	})
	tr.installGuest(t, pkg{name: "alerting", version: "0.5.0"})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	require.Len(t, listing.Packages, 3)
	assert.Equal(t, state.ScopeProject, listing.Scope)
	assert.Equal(t, tr.dir, listing.Root)

	assert.Equal(t, "my-app", listing.Packages[0].Name)
	assert.True(t, listing.Packages[0].Primary)
	assert.Equal(t, "1.2.3", listing.Packages[0].Version)
	assert.Equal(t, "The application", listing.Packages[0].Description)

	assert.Equal(t, "alerting", listing.Packages[1].Name)
	assert.False(t, listing.Packages[1].Primary)

	assert.Equal(t, "monitoring", listing.Packages[2].Name)
	assert.Equal(t, map[string]string{"metrics": "1.0.0"}, listing.Packages[2].Dependencies)
}

// TestListJSONIsValid pins the second half of the task's headline case: the
// machine output parses, and carries the same three packages.
func TestListJSONIsValid(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installPrimary(t, pkg{name: "my-app", version: "1.2.3"})
	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"}})
	tr.installGuest(t, pkg{name: "alerting", version: "0.5.0"})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	rendered := render(t, listing, inventory.FormatJSON)

	var decoded struct {
		Scope    string `json:"scope"`
		Root     string `json:"root"`
		Packages []struct {
			Name         string            `json:"name"`
			Version      string            `json:"version"`
			Primary      bool              `json:"primary"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}

	require.NoError(t, json.Unmarshal([]byte(rendered), &decoded))

	assert.Equal(t, "project", decoded.Scope)
	assert.Equal(t, tr.dir, decoded.Root)
	require.Len(t, decoded.Packages, 3)

	assert.Equal(t, "my-app", decoded.Packages[0].Name)
	assert.True(t, decoded.Packages[0].Primary)
	assert.Equal(t, map[string]string{"metrics": "1.0.0"}, decoded.Packages[2].Dependencies)
}

// TestListEmptyScope pins that a project nothing was installed into lists as
// empty rather than failing. A fresh checkout has no .rocks/ at all.
func TestListEmptyScope(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	assert.Empty(t, listing.Packages)
	assert.Equal(t, state.ScopeProject, listing.Scope)
}

// TestListGuestsOnly covers a tree with guests but no primary package — a
// deploy directory rather than a development one.
func TestListGuestsOnly(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0"})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	require.Len(t, listing.Packages, 1)
	assert.False(t, listing.Packages[0].Primary)
}

// TestListOmitsEmptyDependencies pins that a package that brought nothing has no
// dependencies key at all, rather than an empty object in the machine output.
func TestListOmitsEmptyDependencies(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "alerting", version: "0.5.0"})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	require.Len(t, listing.Packages, 1)
	assert.Nil(t, listing.Packages[0].Dependencies)

	assert.NotContains(t, render(t, listing, inventory.FormatJSON), "dependencies")
}

// TestListTolerantOfBrokenGuest pins that one corrupt metadata directory does
// not take the whole inventory down: list is a diagnostic tool, and it is most
// needed exactly when something is wrong.
func TestListTolerantOfBrokenGuest(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0"})
	tr.write(t,
		filepath.Join(tr.lay.Manifests, "broken", state.MetaManifestFile),
		"not = valid toml [")

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	require.Len(t, listing.Packages, 1)
	assert.Equal(t, "monitoring", listing.Packages[0].Name)
}

// TestListGuestWithoutLock pins that a guest installed before lock metadata
// existed still lists, owning nothing.
func TestListGuestWithoutLock(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "legacy", version: "1.0.0", noLock: true})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	require.Len(t, listing.Packages, 1)
	assert.Equal(t, "legacy", listing.Packages[0].Name)
	assert.Nil(t, listing.Packages[0].Dependencies)
}

// TestListRejectsUnknownScope pins that a bad --scope fails with exit 1 rather
// than silently listing the project tree.
func TestListRejectsUnknownScope(t *testing.T) {
	t.Parallel()

	_, err := inventory.List(inventory.ListOptions{
		ProjectDir: t.TempDir(), Scope: state.Scope("global"),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, state.ErrUnknownScope)
	assert.Equal(t, 1, inventory.ExitCode(err))
}

// TestListDefaultsToProjectScope pins that the zero scope is project, so the
// CLI's default and the library's default cannot drift apart.
func TestListDefaultsToProjectScope(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0"})

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir, Scope: ""})
	require.NoError(t, err)

	assert.Equal(t, state.ScopeProject, listing.Scope)
	require.Len(t, listing.Packages, 1)
}
