package inventory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/inventory"
	"github.com/tarantool/tt/cli/manifest/state"
)

// sharedTree lays out the task's uninstall scenario: two guests, one dependency
// only the first holds, and one both hold.
//
//	monitoring -> metrics (only owner), checks (shared)
//	alerting   -> checks  (shared)
func sharedTree(t *testing.T) tree {
	t.Helper()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0", "checks": "3.1.0"},
	})
	tr.installGuest(t, pkg{
		name: "alerting", version: "0.5.0",
		pins: map[string]string{"checks": "3.1.0"},
	})

	return tr
}

// TestUninstallGuestDropsSoleDependencyKeepsShared is the task's headline case:
// the package's files and metadata go, the dependency only it held goes with
// them, and the one another package still holds stays.
func TestUninstallGuestDropsSoleDependencyKeepsShared(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	// The package itself is gone, files and metadata both.
	assert.False(t, tr.rockExists("monitoring"), "package files must be removed")
	assert.False(t, tr.metadataExists("monitoring"), "package metadata must be removed")

	// The dependency only monitoring held is gone.
	assert.False(t, tr.rockExists("metrics"), "sole-owner dependency must be removed")
	assert.Equal(t, []string{"metrics"}, result.RemovedDependencies)

	// The dependency alerting still holds stays, and the report says who holds it.
	assert.True(t, tr.rockExists("checks"), "shared dependency must survive")
	require.Len(t, result.KeptDependencies, 1)
	assert.Equal(t, "checks", result.KeptDependencies[0].Name)
	assert.Equal(t, []string{"alerting"}, result.KeptDependencies[0].HeldBy)

	// The other package is untouched.
	assert.True(t, tr.rockExists("alerting"))
	assert.True(t, tr.metadataExists("alerting"))

	assert.Equal(t, "monitoring", result.Package)
	assert.Equal(t, "2.0.0", result.Version)
	assert.Equal(t, state.ScopeProject, result.Scope)
}

// TestUninstallLeavesListingConsistent pins the two commands against each other:
// after an uninstall, list no longer reports the package.
func TestUninstallLeavesListingConsistent(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	listing, err := inventory.List(inventory.ListOptions{ProjectDir: tr.dir})
	require.NoError(t, err)

	require.Len(t, listing.Packages, 1)
	assert.Equal(t, "alerting", listing.Packages[0].Name)
}

// TestUninstallKeepsDependencyHeldByPrimary pins that the project's own package
// counts as an owner: a dependency it locks survives a guest's removal even
// though no other guest holds it.
func TestUninstallKeepsDependencyHeldByPrimary(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installPrimary(t, pkg{
		name: "my-app", version: "1.0.0",
		pins: map[string]string{"metrics": "1.0.0"},
	})
	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"},
	})

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	assert.True(t, tr.rockExists("metrics"), "a dependency the project holds must survive")
	assert.Empty(t, result.RemovedDependencies)
	require.Len(t, result.KeptDependencies, 1)
	assert.Equal(t, []string{"my-app"}, result.KeptDependencies[0].HeldBy)
}

// TestUninstallRefusesPrimaryPackage pins the task's refusal: the package the
// project develops is not a guest, and its place in the tree is the project's to
// decide.
func TestUninstallRefusesPrimaryPackage(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installPrimary(t, pkg{name: "my-app", version: "1.0.0"})

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "my-app", Yes: true,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, inventory.ErrPrimaryPackage)
	assert.Equal(t, 1, inventory.ExitCode(err))

	// Nothing was touched.
	assert.True(t, tr.rockExists("my-app"))
}

// TestUninstallRefusesPrimaryEvenWithSameNameGuestAbsent guards the lookup
// order: the primary package is found by name first, so its name cannot be
// smuggled through as if it were a guest.
func TestUninstallRefusesPrimaryEvenWithSameNameGuestAbsent(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installPrimary(t, pkg{
		name: "my-app", version: "1.0.0",
		pins: map[string]string{"metrics": "1.0.0"},
	})

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "my-app", Yes: true,
	})
	require.ErrorIs(t, err, inventory.ErrPrimaryPackage)

	assert.True(t, tr.rockExists("metrics"), "a refusal must remove nothing")
}

// TestUninstallNotInstalled pins the error for a package the scope does not
// hold.
func TestUninstallNotInstalled(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "absent", Yes: true,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, inventory.ErrNotInstalled)
	assert.Equal(t, 1, inventory.ExitCode(err))

	// A miss must not disturb what is installed.
	assert.True(t, tr.rockExists("monitoring"))
	assert.True(t, tr.rockExists("metrics"))
}

// TestUninstallRejectsBadPackageName pins the guard that runs before any
// removal. The name becomes a path, so a traversal-shaped argument must be
// refused rather than resolved.
func TestUninstallRejectsBadPackageName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"../../etc",
		"..",
		".",
		"/absolute",
		"has/slash",
		"Upper",
		"",
		// Reserved: manifests/ is the metadata directory itself, bin/ the tree's
		// executables. Neither is a package that can be removed.
		"manifests",
		"bin",
	} {
		tr := sharedTree(t)

		_, err := inventory.Uninstall(inventory.UninstallOptions{
			ProjectDir: tr.dir, Package: name, Yes: true,
		})
		require.Error(t, err, name)
		require.ErrorIs(t, err, inventory.ErrBadPackageName, name)
		assert.Equal(t, 1, inventory.ExitCode(err), name)

		// The tree is intact.
		assert.True(t, tr.metadataExists("monitoring"), name)
		assert.True(t, tr.rockExists("metrics"), name)
	}
}

// TestUninstallConfirmationDeclinedRemovesNothing pins that declining the prompt
// is a full abort, not a partial removal.
func TestUninstallConfirmationDeclinedRemovesNothing(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring",
		Confirm: func(string) bool { return false },
	})
	require.Error(t, err)
	require.ErrorIs(t, err, inventory.ErrAborted)

	assert.True(t, tr.rockExists("monitoring"))
	assert.True(t, tr.metadataExists("monitoring"))
	assert.True(t, tr.rockExists("metrics"))
}

// TestUninstallConfirmationAccepted covers the other branch, and that the prompt
// names the dependencies that go along — the whole point of asking.
func TestUninstallConfirmationAccepted(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	var prompt string

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring",
		Confirm: func(p string) bool { prompt = p; return true },
	})
	require.NoError(t, err)

	assert.Contains(t, prompt, "monitoring")
	assert.Contains(t, prompt, "metrics")
	assert.NotContains(t, prompt, "checks", "a surviving dependency must not be announced")

	assert.False(t, tr.metadataExists("monitoring"))
}

// TestUninstallYesSkipsConfirmation pins that --yes never asks.
func TestUninstallYesSkipsConfirmation(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	asked := false

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
		Confirm: func(string) bool { asked = true; return false },
	})
	require.NoError(t, err)

	assert.False(t, asked, "--yes must not prompt")
	assert.False(t, tr.metadataExists("monitoring"))
}

// TestUninstallNilConfirmProceeds pins the library default: a caller that wires
// no prompt is not blocked by one.
func TestUninstallNilConfirmProceeds(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring",
	})
	require.NoError(t, err)

	assert.False(t, tr.metadataExists("monitoring"))
}

// TestUninstallGuestWithoutLock covers a guest whose metadata predates locks: it
// owns nothing, so only its own files and metadata go.
func TestUninstallGuestWithoutLock(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{name: "legacy", version: "1.0.0", noLock: true})
	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"},
	})

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "legacy", Yes: true,
	})
	require.NoError(t, err)

	assert.Empty(t, result.RemovedDependencies)
	assert.False(t, tr.rockExists("legacy"))
	assert.True(t, tr.rockExists("metrics"), "another package's dependency is untouched")
}

// TestUninstallDependencyThatIsAlsoAPackage pins the guard that matters most for
// not eating a user's install: a guest that another package happens to depend on
// is removed only when it is named, never as a mere shared rock.
func TestUninstallDependencyThatIsAlsoAPackage(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	// monitoring depends on the "shared-lib" rock, which is also installed as a
	// package in its own right.
	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		pins: map[string]string{"shared-lib": "1.0.0"},
	})
	tr.installGuest(t, pkg{name: "shared-lib", version: "1.0.0"})

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	assert.True(t, tr.rockExists("shared-lib"), "an installed package is not a disposable rock")
	assert.True(t, tr.metadataExists("shared-lib"))
	assert.Empty(t, result.RemovedDependencies)
	require.Len(t, result.KeptDependencies, 1)

	// It is kept for being a package, not for being referenced: nothing pins it
	// once monitoring is gone, and the two reasons are reported apart.
	assert.Equal(t, "shared-lib", result.KeptDependencies[0].Name)
	assert.True(t, result.KeptDependencies[0].Installed)
	assert.Empty(t, result.KeptDependencies[0].HeldBy)
}

// TestUninstallWarnsWhenOthersStillDependOnIt pins that removing a package
// another one locks is allowed — the argument was explicit — but reported, so
// the breakage is not discovered at runtime.
func TestUninstallWarnsWhenOthersStillDependOnIt(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		pins: map[string]string{"shared-lib": "1.0.0"},
	})
	tr.installGuest(t, pkg{name: "shared-lib", version: "1.0.0"})

	var warnings []string

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "shared-lib", Yes: true,
		Warn: func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.False(t, tr.rockExists("shared-lib"), "an explicit removal still goes through")
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0], "monitoring")
}

// TestUninstallIsRepeatable pins the ordering rule: files go before metadata, so
// an uninstall that is run twice — or resumed after an interruption — converges
// instead of failing.
func TestUninstallIsRepeatable(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	opts := inventory.UninstallOptions{ProjectDir: tr.dir, Package: "monitoring", Yes: true}

	_, err := inventory.Uninstall(opts)
	require.NoError(t, err)

	// The second run finds nothing left to remove, which is a clean "not
	// installed" rather than a crash.
	_, err = inventory.Uninstall(opts)
	require.ErrorIs(t, err, inventory.ErrNotInstalled)

	assert.True(t, tr.rockExists("checks"), "the shared dependency is still not collateral")
}

// TestUninstallPrunesEmptyRockRoot pins that no husk is left under rocks/<name>/
// once the last version is gone — a leftover empty directory would make the rock
// read as installed to the next plan.
func TestUninstallPrunesEmptyRockRoot(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	_, found := state.InstalledVersion(tr.lay, "metrics")
	assert.False(t, found, "the removed dependency must not read as installed")

	_, found = state.InstalledVersion(tr.lay, "monitoring")
	assert.False(t, found, "the removed package must not read as installed")

	version, found := state.InstalledVersion(tr.lay, "checks")
	require.True(t, found, "the surviving dependency must still read as installed")
	assert.Equal(t, "3.1.0", version)
}

// TestUninstallRemovesVersionFoundOnDisk pins that the tree wins over the lock
// when they disagree. A later install can reconcile a dependency to another
// version without rewriting the earlier package's lock, and it is the files that
// actually have to go.
func TestUninstallRemovesVersionFoundOnDisk(t *testing.T) {
	t.Parallel()

	tr := newTree(t)

	tr.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"},
	})

	// Simulate a reconciliation: the tree now carries 1.4.0 while the lock still
	// records 1.0.0.
	require.NoError(t, removeAll(tr.rockManifestPath("metrics", "1.0.0")))
	tr.installRock(t, "metrics", "1.4.0")

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Package: "monitoring", Yes: true,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"metrics"}, result.RemovedDependencies)
	assert.False(t, tr.rockExists("metrics"), "the on-disk version must be the one removed")
}

// TestUninstallRejectsUnknownScope pins that a bad --scope fails before any
// lookup.
func TestUninstallRejectsUnknownScope(t *testing.T) {
	t.Parallel()

	tr := sharedTree(t)

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: tr.dir, Scope: state.Scope("global"), Package: "monitoring", Yes: true,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, state.ErrUnknownScope)
	assert.Equal(t, 1, inventory.ExitCode(err))

	assert.True(t, tr.metadataExists("monitoring"))
}
