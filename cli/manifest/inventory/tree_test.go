package inventory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/inventory"
	"github.com/tarantool/tt/cli/manifest/state"
)

// treeFixture lays out every case the dependency view has to distinguish, at
// both depths. A lock carries the whole transitive closure, so monitoring's lock
// pins four rocks while its manifest declares only two:
//
//	metrics    — declared by monitoring, held only by it (would go with it)
//	shared-lib — declared by monitoring, but installed in its own right
//	checks     — transitive for monitoring, declared by my-app and alerting
//	luasocket  — transitive for monitoring, held only by it
func treeFixture(t *testing.T) tree {
	t.Helper()

	tr := newTree(t)

	tr.installPrimary(t, pkg{
		name: "my-app", version: "1.2.3",
		deps: map[string]string{"checks": ">=3.0.0"},
		pins: map[string]string{"checks": "3.1.0"},
	})
	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		deps: map[string]string{"metrics": ">=1.0.0", "shared-lib": ">=1.0.0"},
		pins: map[string]string{
			"metrics": "1.0.0", "shared-lib": "1.0.0",
			"checks": "3.1.0", "luasocket": "3.0.4",
		},
	})
	tr.installGuest(t, pkg{
		name: "alerting", version: "0.5.0",
		deps: map[string]string{"checks": ">=3.0.0"},
		pins: map[string]string{"checks": "3.1.0"},
	})
	tr.installGuest(t, pkg{name: "shared-lib", version: "1.0.0"})

	// The edges LuaRocks records in the tree: metrics pulls checks, shared-lib
	// pulls luasocket. Those two are exactly monitoring's transitive rocks.
	tr.writeTreeManifest(t,
		map[string][]string{
			"metrics":    {"checks"},
			"shared-lib": {"luasocket"},
		},
		map[string]string{"metrics": "1.0.0", "shared-lib": "1.0.0"})

	return tr
}

// listTree reads the inventory with the dependency index populated.
func listTree(t *testing.T, tr tree) *inventory.Listing {
	t.Helper()

	listing, err := inventory.List(inventory.ListOptions{
		ProjectDir: tr.dir, Rocks: true,
	})
	require.NoError(t, err)

	return listing
}

// TestRockIndexOwners pins the reverse index: every rock with the packages
// that brought it, in name order.
func TestRockIndexOwners(t *testing.T) {
	t.Parallel()

	listing := listTree(t, treeFixture(t))

	byName := map[string]inventory.RockEntry{}
	for _, dep := range listing.Rocks {
		byName[dep.Name] = dep
	}

	names := make([]string, 0, len(listing.Rocks))
	for _, dep := range listing.Rocks {
		names = append(names, dep.Name)
	}

	assert.Equal(t, []string{"checks", "luasocket", "metrics", "shared-lib"}, names,
		"the index is in name order and spans the whole closure, not just declarations")

	// checks is transitive for monitoring and declared by the other two; ownership
	// does not care which, so all three own it.
	assert.Equal(t, []string{"alerting", "monitoring", "my-app"}, byName["checks"].Owners)
	assert.True(t, byName["checks"].Shared)

	assert.Equal(t, []string{"monitoring"}, byName["metrics"].Owners)
	assert.False(t, byName["metrics"].Shared)
	assert.Equal(t, "1.0.0", byName["metrics"].Version)

	// A purely transitive rock is owned exactly like a declared one.
	assert.Equal(t, []string{"monitoring"}, byName["luasocket"].Owners)
	assert.False(t, byName["luasocket"].Shared)
}

// TestRockIndexMarksInstalledPackages pins that a dependency which is also
// an installed package is flagged: it survives on its own metadata, so the
// shared/exclusive reading does not apply to it.
func TestRockIndexMarksInstalledPackages(t *testing.T) {
	t.Parallel()

	listing := listTree(t, treeFixture(t))

	byName := map[string]inventory.RockEntry{}
	for _, dep := range listing.Rocks {
		byName[dep.Name] = dep
	}

	assert.True(t, byName["shared-lib"].Installed)
	assert.False(t, byName["metrics"].Installed)
	assert.False(t, byName["checks"].Installed)
}

// TestRockIndexAbsentByDefault pins that a plain listing does not carry
// the index — it is computed only when asked for.
func TestRockIndexAbsentByDefault(t *testing.T) {
	t.Parallel()

	tr := treeFixture(t)

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	assert.Nil(t, listing.Rocks)
	assert.NotContains(t, render(t, listing, inventory.FormatJSON), `"rocks": [`)
}

// TestRockIndexEmptyScope pins that a scope with nothing installed yields
// an empty index rather than a nil-versus-empty surprise in the renderer.
func TestRockIndexEmptyScope(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	listing := listTree(t, tr)

	assert.Empty(t, listing.Rocks)
	assert.Empty(t, listing.Packages)
}

// TestRockIndexPrefersOnDiskVersion pins that the index reports the
// version in the tree, not a lock's pin, when the two disagree. A later install
// can reconcile a shared rock to a version an earlier lock never named, and the
// tree is what an uninstall would remove.
func TestRockIndexPrefersOnDiskVersion(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0", pins: map[string]string{
		"metrics": "1.0.0",
	}})

	require.NoError(t, removeAll(tr.rockManifestPath("metrics", "1.0.0")))
	tr.installRock(t, "metrics", "1.4.0")

	listing := listTree(t, tr)

	require.Len(t, listing.Rocks, 1)
	assert.Equal(t, "metrics", listing.Rocks[0].Name)
	assert.Equal(t, "1.4.0", listing.Rocks[0].Version)
}

// TestRockIndexFallsBackToPin pins the other side of that rule: a
// dependency a lock records but that has no files reports the pin, so it is not
// silently versionless.
func TestRockIndexFallsBackToPin(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "monitoring", version: "2.0.0", pins: map[string]string{
		"metrics": "1.0.0",
	}})
	require.NoError(t, removeAll(tr.rockManifestPath("metrics", "1.0.0")))

	listing := listTree(t, tr)

	require.Len(t, listing.Rocks, 1)
	assert.Equal(t, "1.0.0", listing.Rocks[0].Version)
}

// TestDirectIsSubsetOfClosure pins the depth split: a lock carries the whole
// transitive closure, and Direct names the part the manifest actually asked for.
func TestDirectIsSubsetOfClosure(t *testing.T) {
	t.Parallel()

	listing := listTree(t, treeFixture(t))

	byName := map[string]inventory.Entry{}
	for _, entry := range listing.Packages {
		byName[entry.Name] = entry
	}

	monitoring := byName["monitoring"]
	assert.Len(t, monitoring.Dependencies, 4, "the closure is what the lock pins")
	assert.Equal(t, []string{"metrics", "shared-lib"}, monitoring.Direct,
		"only the declared rocks are direct, in name order")

	assert.Equal(t, []string{"checks"}, byName["my-app"].Direct)
	assert.Empty(t, byName["shared-lib"].Direct)
}

// TestDirectIgnoresUndeclaredAndUnresolved pins that Direct stays a subset of
// what is installed. A manifest can declare a dependency the lock never pinned —
// unresolved, or dev-only — and that must not appear as an installed direct
// dependency.
func TestDirectIgnoresUndeclaredAndUnresolved(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		// Declares two, but only one made it into the lock.
		deps: map[string]string{"metrics": ">=1.0.0", "never-resolved": ">=9.0.0"},
		pins: map[string]string{"metrics": "1.0.0", "luasocket": "3.0.4"},
	})

	listing := listTree(t, tr)

	require.Len(t, listing.Packages, 1)
	assert.Equal(t, []string{"metrics"}, listing.Packages[0].Direct)
	assert.NotContains(t, listing.Packages[0].Dependencies, "never-resolved")
}

// TestDeclaredInComponentCountsAsDirect pins that a dependency declared by a
// component, not the global map, is direct too — the resolver treats both as
// declarations, so the view must agree.
func TestDeclaredInComponentCountsAsDirect(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuestManifest(t, "monitoring", "2.0.0",
		manifestWithComponentDep("monitoring", "metrics", ">=1.0.0"),
		map[string]string{"metrics": "1.0.0", "luasocket": "3.0.4"})

	listing := listTree(t, tr)

	require.Len(t, listing.Packages, 1)
	assert.Equal(t, []string{"metrics"}, listing.Packages[0].Direct)
}

// TestTransitiveOnlyDependencyIsRefcounted is the correctness case behind the
// whole depth question: ownership is counted over the closure, not over
// declarations. A rock nothing declares but two packages pulled in is shared and
// must survive one of them going.
func TestTransitiveOnlyDependencyIsRefcounted(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	// Neither package declares luasocket; both locks pin it transitively.
	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		deps: map[string]string{"metrics": ">=1.0.0"},
		pins: map[string]string{"metrics": "1.0.0", "luasocket": "3.0.4"},
	})
	tr.installGuest(t, pkg{
		name: "alerting", version: "0.5.0",
		deps: map[string]string{"checks": ">=3.0.0"},
		pins: map[string]string{"checks": "3.1.0", "luasocket": "3.0.4"},
	})

	listing := listTree(t, tr)

	byName := map[string]inventory.RockEntry{}
	for _, dep := range listing.Rocks {
		byName[dep.Name] = dep
	}

	assert.Equal(t, []string{"alerting", "monitoring"}, byName["luasocket"].Owners)
	assert.True(t, byName["luasocket"].Shared,
		"a rock nobody declared is still owned by everyone whose lock pins it")

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"metrics"}, result.RemovedDependencies)
	assert.True(t, tr.rockExists("luasocket"),
		"a transitive rock another package still pulls in must survive")

	require.Len(t, result.KeptDependencies, 1)
	assert.Equal(t, "luasocket", result.KeptDependencies[0].Name)
	assert.Equal(t, []string{"alerting"}, result.KeptDependencies[0].HeldBy)
}

// TestTransitiveOnlyDependencyIsRemovedWithItsLastOwner is the other half: the
// same rock goes when nothing else pulls it in, even though no manifest ever
// named it.
func TestTransitiveOnlyDependencyIsRemovedWithItsLastOwner(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		deps: map[string]string{"metrics": ">=1.0.0"},
		pins: map[string]string{"metrics": "1.0.0", "luasocket": "3.0.4"},
	})

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"luasocket", "metrics"}, result.RemovedDependencies)
	assert.False(t, tr.rockExists("luasocket"))
	assert.False(t, tr.rockExists("metrics"))
}

// TestRenderTreeNestsUnderTheRequiringRock is the point of reading the tree
// manifest: a rock hangs off whatever pulled it in, not off a group.
//
// The lock alone cannot say this — it stores the closure flattened, with the
// parent thrown away — but LuaRocks records each installed rock's own resolved
// requirements in the tree manifest, so the edges are recoverable from the tree.
func TestRenderTreeNestsUnderTheRequiringRock(t *testing.T) {
	t.Parallel()

	out := render(t, listTree(t, treeFixture(t)), inventory.FormatTable)

	metrics := subtreeOf(t, out, "metrics 1.0.0")
	require.Len(t, metrics, 2, "metrics carries the rock it requires")
	assert.Contains(t, treeLabel(metrics[1]), "checks 3.1.0")

	sharedLib := subtreeOf(t, out, "shared-lib 1.0.0 (installed package)")
	require.Len(t, sharedLib, 2)
	assert.Contains(t, treeLabel(sharedLib[1]), "luasocket 3.0.4")

	// Nothing is left over, so the unknown-parent group does not appear at all.
	assert.NotContains(t, out, transitiveGroupLabel)
}

// TestRenderTreeEdgesAreScopedToThePackage pins that the tree manifest is read
// as scope-wide data, not as one package's graph: a rock another package brought
// must not be grafted under this one just because an edge exists.
func TestRenderTreeEdgesAreScopedToThePackage(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	// alerting requires checks; monitoring requires only metrics. The edge
	// metrics -> checks exists tree-wide, but checks is not in monitoring's
	// closure, so it must not appear beneath it.
	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		deps: map[string]string{"metrics": ">=1.0.0"},
		pins: map[string]string{"metrics": "1.0.0"},
	})
	tr.installGuest(t, pkg{
		name: "alerting", version: "0.5.0",
		deps: map[string]string{"checks": ">=3.0.0"},
		pins: map[string]string{"checks": "3.1.0"},
	})
	tr.writeTreeManifest(t,
		map[string][]string{"metrics": {"checks"}},
		map[string]string{"metrics": "1.0.0"})

	out := render(t, listTree(t, tr), inventory.FormatTable)

	monitoring := subtreeOf(t, out, "monitoring 2.0.0")
	assert.NotContains(t, strings.Join(monitoring, "\n"), "checks",
		"a rock outside this package's closure is not grafted under it")
}

// TestRenderTreeFallsBackWithoutTreeManifest pins the graceful path: a tree that
// carries no LuaRocks manifest has no edges to walk, so indirect rocks are still
// listed — under a group that says their parent is not recorded.
func TestRenderTreeFallsBackWithoutTreeManifest(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		deps: map[string]string{"metrics": ">=1.0.0"},
		pins: map[string]string{"metrics": "1.0.0", "luasocket": "3.0.4"},
	})

	out := render(t, listTree(t, tr), inventory.FormatTable)

	assert.Contains(t, out, transitiveGroupLabel)
	assert.Contains(t, out, "luasocket 3.0.4 (only here)",
		"an unreachable rock is still shown, not dropped")
}

// TestRenderTreeSurvivesCyclicEdges pins that a malformed tree manifest cannot
// hang the view. LuaRocks closures are acyclic in principle, but this is data on
// disk and the walk must terminate regardless.
func TestRenderTreeSurvivesCyclicEdges(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		deps: map[string]string{"metrics": ">=1.0.0"},
		pins: map[string]string{"metrics": "1.0.0", "luasocket": "3.0.4"},
	})
	// metrics -> luasocket -> metrics.
	tr.writeTreeManifest(t,
		map[string][]string{"metrics": {"luasocket"}, "luasocket": {"metrics"}},
		map[string]string{"metrics": "1.0.0", "luasocket": "3.0.4"})

	out := render(t, listTree(t, tr), inventory.FormatTable)

	assert.Contains(t, out, "metrics 1.0.0")
	assert.Contains(t, out, "luasocket 3.0.4")
}

// TestRenderTreeWithoutTransitiveRocks pins that a package whose closure is
// entirely declared shows no empty group.
func TestRenderTreeWithoutTransitiveRocks(t *testing.T) {
	t.Parallel()

	out := render(t, listTree(t, treeFixture(t)), inventory.FormatTable)

	assert.NotContains(t, subtreeText(t, out, "alerting 0.5.0"), "pulled in transitively")

	// my-app's own closure is entirely declared too. Its subtree is the whole
	// tree now that it is the root, so check its own rocks rather than its
	// guests' — those are separate packages with closures of their own.
	for _, line := range treeLines(out) {
		if treeDepth(line) == 1 && !strings.Contains(line, "(guest)") {
			assert.NotContains(t, line, transitiveGroupLabel)
		}
	}
}

// transitiveGroupLabel is the heading the renderer uses for indirect rocks.
const transitiveGroupLabel = "pulled in transitively"

// TestRenderTreeAllTransitive pins the opposite shape: a package whose manifest
// is unreadable declares nothing, so its whole closure is reported as transitive
// rather than silently promoted to direct.
func TestRenderTreeAllTransitive(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"},
	})

	out := render(t, listTree(t, tr), inventory.FormatTable)

	assert.Contains(t, out, "pulled in transitively")
	assert.Contains(t, out, "metrics 1.0.0 (only here)")
}

// TestRenderTreeHasSingleRoot pins the shape: one tree, rooted at the project's
// own package. Guests are installed into that project's tree, so they hang off
// it rather than standing as separate roots.
func TestRenderTreeHasSingleRoot(t *testing.T) {
	t.Parallel()

	lines := treeLines(render(t, listTree(t, treeFixture(t)), inventory.FormatTable))

	var roots []string

	for _, line := range lines {
		if treeDepth(line) == 0 {
			roots = append(roots, treeLabel(line))
		}
	}

	require.Len(t, roots, 1, "the tree must have exactly one root:\n%s",
		strings.Join(lines, "\n"))
	assert.Equal(t, "my-app 1.2.3 (primary)", roots[0])
}

// TestRenderTreeGuestsHangOffPrimary pins that every guest is a child of the
// root, marked as a guest rather than presented as something the project
// requires — the project does not depend on them, it merely contains them.
func TestRenderTreeGuestsHangOffPrimary(t *testing.T) {
	t.Parallel()

	lines := treeLines(render(t, listTree(t, treeFixture(t)), inventory.FormatTable))

	var children []string

	for _, line := range lines {
		if treeDepth(line) == 1 {
			children = append(children, treeLabel(line))
		}
	}

	// The project's own rock first, then the guests in listing order.
	assert.Equal(t, []string{
		"checks 3.1.0 (shared with alerting, monitoring)",
		"alerting 0.5.0 (guest)",
		"monitoring 2.0.0 (guest)",
		"shared-lib 1.0.0 (guest)",
	}, children)
}

// TestRenderTreeShape pins the whole rendering at once, prefixes included, so a
// change to the drawing has to be deliberate.
func TestRenderTreeShape(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{
		"my-app 1.2.3 (primary)",
		"├── checks 3.1.0 (shared with alerting, monitoring)",
		"├── alerting 0.5.0 (guest)",
		"│   └── checks 3.1.0 (shared with monitoring, my-app)",
		"├── monitoring 2.0.0 (guest)",
		"│   ├── metrics 1.0.0 (only here)",
		"│   │   └── checks 3.1.0 (shared with alerting, my-app)",
		"│   └── shared-lib 1.0.0 (installed package)",
		"│       └── luasocket 3.0.4 (only here)",
		"└── shared-lib 1.0.0 (guest)",
		"    └── (no dependencies)",
	}, treeLines(render(t, listTree(t, treeFixture(t)), inventory.FormatTable)))
}

// TestRenderTreeWithoutPrimary pins the rootless case: a shared tree or a deploy
// directory has no project package, so the root says so and the guests still
// hang off one node instead of scattering.
func TestRenderTreeWithoutPrimary(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		deps: map[string]string{"metrics": ">=1.0.0"},
		pins: map[string]string{"metrics": "1.0.0"},
	})
	tr.installGuest(t, pkg{name: "alerting", version: "0.5.0"})

	lines := treeLines(render(t, listTree(t, tr), inventory.FormatTable))

	require.Equal(t, "(no project package)", lines[0])

	var roots int

	for _, line := range lines {
		if treeDepth(line) == 0 {
			roots++
		}
	}

	assert.Equal(t, 1, roots)
	assert.Equal(t, "├── alerting 0.5.0 (guest)", lines[1])
	assert.Equal(t, "└── monitoring 2.0.0 (guest)", lines[3])
}

// TestRenderTreeContinuationBars pins that a nested level stays visually
// attached: while siblings follow, deeper lines carry the vertical bar; once the
// branch closes they are plain indentation.
func TestRenderTreeContinuationBars(t *testing.T) {
	t.Parallel()

	lines := treeLines(render(t, listTree(t, treeFixture(t)), inventory.FormatTable))

	// monitoring is not the last child, so its rocks keep the bar.
	assert.Contains(t, lines, "│   ├── metrics 1.0.0 (only here)")
	// shared-lib is the last child, so its rock is plainly indented.
	assert.Contains(t, lines, "    └── (no dependencies)")
}

// TestRenderTreeAnnotatesEveryDependency is the view's whole point: each branch
// says whether the dependency would leave with its package or stay.
func TestRenderTreeAnnotatesEveryDependency(t *testing.T) {
	t.Parallel()

	out := render(t, listTree(t, treeFixture(t)), inventory.FormatTable)

	assert.Contains(t, out, "my-app 1.2.3 (primary)")

	// Under monitoring: one shared, one exclusive, one an installed package.
	monitoring := subtreeText(t, out, "monitoring 2.0.0")
	assert.Contains(t, monitoring, "checks 3.1.0 (shared with alerting, my-app)")
	assert.Contains(t, monitoring, "metrics 1.0.0 (only here)")
	assert.Contains(t, monitoring, "shared-lib 1.0.0 (installed package)")

	// The owner is never listed as sharing with itself.
	assert.NotContains(t, monitoring, "shared with monitoring")

	// A package that brought nothing says so rather than showing a bare header.
	// The guest node is named explicitly: "shared-lib 1.0.0" also matches the
	// dependency line under monitoring.
	assert.Contains(t, subtreeText(t, out, "shared-lib 1.0.0 (guest)"), "(no dependencies)")
}

// TestRenderTreeEmptyScope pins that an empty scope falls back to the plain
// sentence instead of printing a header with nothing under it.
func TestRenderTreeEmptyScope(t *testing.T) {
	t.Parallel()

	out := render(t, listTree(t, newTree(t)), inventory.FormatTable)

	assert.Contains(t, out, "no packages installed")
	assert.NotContains(t, out, "├")
	assert.NotContains(t, out, "└")
}

// TestTreeInMachineFormat pins that --tree is not a table-only view: the index
// travels in the machine output too, which is what makes it scriptable.
func TestTreeInMachineFormat(t *testing.T) {
	t.Parallel()

	rendered := render(t, listTree(t, treeFixture(t)), inventory.FormatJSON)

	var decoded struct {
		Rocks []struct {
			Name      string   `json:"name"`
			Version   string   `json:"version"`
			Owners    []string `json:"owners"`
			Shared    bool     `json:"shared"`
			Installed bool     `json:"installed"`
		} `json:"rocks"`
	}

	require.NoError(t, json.Unmarshal([]byte(rendered), &decoded))
	require.Len(t, decoded.Rocks, 4)

	byName := map[string]bool{}
	for _, dep := range decoded.Rocks {
		byName[dep.Name] = dep.Shared
	}

	assert.Equal(t, "checks", decoded.Rocks[0].Name)
	assert.True(t, decoded.Rocks[0].Shared)
	assert.Equal(t, []string{"alerting", "monitoring", "my-app"}, decoded.Rocks[0].Owners)
	assert.False(t, byName["luasocket"], "a transitive rock with one owner is not shared")
	assert.Equal(t, "shared-lib", decoded.Rocks[3].Name)
	assert.True(t, decoded.Rocks[3].Installed)
}

// TestTreeMatchesUninstallOutcome is the guarantee the view is worth having: what
// the tree says would happen is what uninstall actually does.
func TestTreeMatchesUninstallOutcome(t *testing.T) {
	t.Parallel()

	tr := treeFixture(t)

	before := subtreeText(t, render(t, listTree(t, tr), inventory.FormatTable), "monitoring 2.0.0")
	require.Contains(t, before, "metrics 1.0.0 (only here)")
	require.Contains(t, before, "luasocket 3.0.4 (only here)")
	require.Contains(t, before, "checks 3.1.0 (shared with alerting, my-app)")

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	// Both "only here" rocks were removed, the declared one and the transitive
	// one alike; "shared with" and the installed package stayed.
	assert.Equal(t, []string{"luasocket", "metrics"}, result.RemovedDependencies)
	assert.False(t, tr.rockExists("metrics"))
	assert.False(t, tr.rockExists("luasocket"))
	assert.True(t, tr.rockExists("checks"))
	assert.True(t, tr.rockExists("shared-lib"))
}

// TestTreeScopeIsReported pins that the tree header names the tree it describes,
// the same fact the machine output carries.
func TestTreeScopeIsReported(t *testing.T) {
	t.Parallel()

	tr := treeFixture(t)
	listing := listTree(t, tr)

	out := render(t, listing, inventory.FormatTable)

	assert.Contains(t, out, string(state.ScopeProject))
	assert.Contains(t, out, tr.dir)
}

// treeLines returns the rendered tree without the scope header, so line indices
// are indices into the tree itself.
func treeLines(out string) []string {
	_, tree, _ := strings.Cut(out, "\n\n")

	return strings.Split(strings.TrimRight(tree, "\n"), "\n")
}

// treeDepth reports how deep a rendered line sits: 0 for the root, 1 for its
// children, and so on. The prefix is built from fixed four-rune groups, so the
// depth is just how many of them precede the label.
func treeDepth(line string) int {
	runes := []rune(line)

	for n := 0; n+4 <= len(runes); n += 4 {
		switch string(runes[n : n+4]) {
		case "│   ", "    ":
			continue
		case "├── ", "└── ":
			return n/4 + 1
		default:
			return 0
		}
	}

	return 0
}

// treeLabel strips a line's prefix, leaving the node's own text.
func treeLabel(line string) string {
	runes := []rune(line)

	for n := 0; n+4 <= len(runes); n += 4 {
		switch string(runes[n : n+4]) {
		case "│   ", "    ":
			continue
		case "├── ", "└── ":
			return string(runes[n+4:])
		default:
			return line
		}
	}

	return line
}

// subtreeOf returns the node whose label starts with prefix, together with every
// line nested under it.
func subtreeOf(t *testing.T, out, prefix string) []string {
	t.Helper()

	lines := treeLines(out)

	for i, line := range lines {
		if !strings.HasPrefix(treeLabel(line), prefix) {
			continue
		}

		depth := treeDepth(line)
		subtree := []string{line}

		for _, next := range lines[i+1:] {
			if treeDepth(next) <= depth {
				break
			}

			subtree = append(subtree, next)
		}

		return subtree
	}

	t.Fatalf("no %q node in:\n%s", prefix, out)

	return nil
}

// subtreeText joins a subtree for substring assertions.
func subtreeText(t *testing.T, out, prefix string) string {
	t.Helper()

	return strings.Join(subtreeOf(t, out, prefix), "\n")
}
