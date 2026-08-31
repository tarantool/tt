package resolve_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
	"github.com/tarantool/tt/cli/manifest/rocks"
)

// twoProducts declares one rock in two components, each its own product, so a
// pin can be observed acting on two independent closures that share the
// resolution cache. The constraints differ on purpose: the pin fits p1 and not
// p2.
const twoProducts = `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[components.c1]
path = 'a'
[components.c1.dependencies]
common = '>=1.0.0'
[components.c2]
path = 'b'
[components.c2.dependencies]
common = '>=2.0.0'
[products.p1]
components = ['c1']
default = true
[products.p2]
components = ['c2']
`

// threeVersions is the registry every "pin an older version" test resolves
// against: one rock published three times.
func threeVersions() *fakeAdapter {
	return newFakeAdapter().
		add("metrics", "1.0.0-1", "aaa").
		add("metrics", "1.5.0-1", "bbb").
		add("metrics", "2.0.0-1", "ccc")
}

// TestResolvePinnedWithoutPinsPicksNewest is the regression guard on the
// behaviour pinning is layered over: with no pin set, resolution is still
// newest-that-fits, and Resolve is exactly that call.
func TestResolvePinnedWithoutPinsPicksNewest(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(threeVersions(), "", "tt 3.4.0")

	pinnedLock, warnings, err := engine.ResolvePinned(context.Background(), man, nil)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, "2.0.0-1",
		findDep(t, pinnedLock.Products["default"].Dependencies, "metrics").Version)

	plainLock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Equal(t, pinnedLock.Products, plainLock.Products,
		"Resolve must be ResolvePinned with an empty pin set")
}

// TestPinHoldsOlderVersion is the core promise: a version the lock already
// chose is kept even though the registry has newer ones that also fit.
func TestPinHoldsOlderVersion(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(threeVersions(), "", "tt 3.4.0")

	lock, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"metrics": "1.0.0-1"})
	require.NoError(t, err)
	assert.Empty(t, warnings, "an honored pin is not a diagnostic")

	got := findDep(t, lock.Products["default"].Dependencies, "metrics")
	assert.Equal(t, "1.0.0-1", got.Version)
	assert.Equal(t, "md5:aaa", got.Checksum, "the checksum must follow the pinned version")
}

// TestPinViolatingConstraintsIsDropped covers the manifest moving out from
// under the lock - the shape `tt package add` produces when the edit tightens a
// constraint. The pin loses, the newest that fits wins, and the caller is told.
func TestPinViolatingConstraintsIsDropped(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.5.0'
`)

	engine := resolve.NewEngine(threeVersions(), "", "tt 3.4.0")

	lock, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"metrics": "1.0.0-1"})
	require.NoError(t, err, "a pin that does not fit must never fail the resolution")

	assert.Equal(t, "2.0.0-1",
		findDep(t, lock.Products["default"].Dependencies, "metrics").Version)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "metrics")
	assert.Contains(t, warnings[0], "1.0.0-1")
}

// TestPinRegistryNoLongerServesFallsBack covers the other way a pin stops
// fitting: the constraints are unchanged, but the version the lock recorded is
// no longer published.
func TestPinRegistryNoLongerServesFallsBack(t *testing.T) {
	t.Parallel()

	// 1.0.0-1 was yanked; only the two later versions remain.
	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "bbb").
		add("metrics", "2.0.0-1", "ccc")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"metrics": "1.0.0-1"})
	require.NoError(t, err)

	assert.Equal(t, "2.0.0-1",
		findDep(t, lock.Products["default"].Dependencies, "metrics").Version)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "metrics")
}

// TestPinDoesNotMaskAMissingRock guards the narrowness of the fallback: the
// retry exists to un-pin, not to soften a real failure, so a rock no server has
// at all still fails - with the adapter's own sentinel intact.
func TestPinDoesNotMaskAMissingRock(t *testing.T) {
	t.Parallel()

	man := parseManifest(t, oneProduct+`[dependencies]
ghost = '>=1.0.0'
`)

	engine := resolve.NewEngine(newFakeAdapter(), "", "tt 3.4.0")

	_, _, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"ghost": "1.0.0-1"})
	require.ErrorIs(t, err, rocks.ErrNotFound)
	assert.Contains(t, err.Error(), "ghost")
}

// TestPinOnTransitiveDependencyIsHonored is what makes `tt package update
// <name>` mean anything: holding only the direct dependencies steady would let
// the whole transitive closure drift on every re-resolve.
func TestPinOnTransitiveDependencyIsHonored(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("alpha", "1.0.0-1", "a", dep(t, "common", ">=1.0")).
		add("common", "1.0.0-1", "c10").
		add("common", "1.2.0-1", "c12")

	man := parseManifest(t, oneProduct+`[dependencies]
alpha = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"common": "1.0.0-1"})
	require.NoError(t, err)
	assert.Empty(t, warnings)

	dependencies := lock.Products["default"].Dependencies
	// common is reached only through alpha; unpinned it would be 1.2.0-1.
	assert.Equal(t, "1.0.0-1", findDep(t, dependencies, "common").Version)
	assert.Equal(t, "1.0.0-1", findDep(t, dependencies, "alpha").Version)
}

// TestPinConflictingWithLaterEdgeIsDropped covers the case the greedy walk
// cannot see coming: the pin satisfies the edge that resolves the rock and is
// only contradicted by an edge found later, when its subtree is already walked.
// The pin must still give way rather than turn a resolvable manifest into a
// conflict - this is the `tt package add beta` shape, where beta needs more
// than the lock happens to hold.
func TestPinConflictingWithLaterEdgeIsDropped(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("alpha", "1.0.0-1", "a", dep(t, "common", ">=1.0")).
		add("beta", "1.0.0-1", "b", dep(t, "common", ">=1.1")).
		add("common", "1.0.0-1", "c10").
		add("common", "1.2.0-1", "c12")

	man := parseManifest(t, oneProduct+`[dependencies]
alpha = '>=1.0.0'
beta = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"common": "1.0.0-1"})
	require.NoError(t, err, "a pin must never make a resolvable manifest fail")

	assert.Equal(t, "1.2.0-1",
		findDep(t, lock.Products["default"].Dependencies, "common").Version)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "common")
	assert.Contains(t, warnings[0], "1.0.0-1")
}

// TestGenuineConflictStillErrorsWithPins is the other side of the retry loop:
// dropping pins must not turn an unsatisfiable dependency graph into a silent
// success. Here no pin is honored at all, and the conflict between the two
// branches stands.
func TestGenuineConflictStillErrorsWithPins(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("alpha", "1.0.0-1", "a", dep(t, "common", ">=2.0")).
		add("beta", "1.0.0-1", "b", dep(t, "common", "<2.0")).
		add("common", "1.0.0-1", "c10").
		add("common", "2.0.0-1", "c20")

	man := parseManifest(t, oneProduct+`[dependencies]
alpha = '>=1.0.0'
beta = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	_, _, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"common": "1.0.0-1"})
	require.ErrorIs(t, err, resolve.ErrConflict)
	assert.Contains(t, err.Error(), "common")
}

// TestPinnedProductsDoNotShareTheWrongCacheEntry is the cache guard. Both
// products want the same rock and share one resolution cache, but the pin fits
// only p1's constraints: p1 must get the pinned version and p2 the newest that
// fits its own, neither served the other's answer.
func TestPinnedProductsDoNotShareTheWrongCacheEntry(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("common", "1.0.0-1", "c10").
		add("common", "2.0.0-1", "c20").
		add("common", "3.0.0-1", "c30")

	man := parseManifest(t, twoProducts)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"common": "1.0.0-1"})
	require.NoError(t, err)

	assert.Equal(t, "1.0.0-1",
		findDep(t, lock.Products["p1"].Dependencies, "common").Version,
		"p1 allows the pinned version and must keep it")
	assert.Equal(t, "3.0.0-1",
		findDep(t, lock.Products["p2"].Dependencies, "common").Version,
		"p2 excludes the pinned version and must resolve newest-that-fits")

	// The drop is reported once for the run, not once per product that hits it.
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "common")
}

// TestPinnedSharedDependencyResolvesOnce guards the other half of the cache
// contract: two products asking for the same rock under the same pin and the
// same constraints must still cost one adapter round trip.
func TestPinnedSharedDependencyResolvesOnce(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("common", "1.0.0-1", "c10").
		add("common", "2.0.0-1", "c20")

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
common = '>=1.0.0'
[components.c1]
path = 'a'
[components.c2]
path = 'b'
[products.p1]
components = ['c1']
default = true
[products.p2]
components = ['c2']
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"common": "1.0.0-1"})
	require.NoError(t, err)

	assert.Equal(t, "1.0.0-1",
		findDep(t, lock.Products["p1"].Dependencies, "common").Version)
	assert.Equal(t, "1.0.0-1",
		findDep(t, lock.Products["p2"].Dependencies, "common").Version)
	assert.Equal(t, 1, fake.resolves["common"],
		"a pinned requirement shared by two products must resolve once")
}

// TestPinsIgnorePathDependencies guards that a version pin cannot reach a path
// dependency, which is pinned by path and content hash and has no registry
// version to prefer.
func TestPinsIgnorePathDependencies(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	vendor := writePathDep(t, projectDir, filepath.Join("vendor", "foo"))

	fake := newFakeAdapter().
		add("foo", "9.9.9-1", "registry-foo"). // Must never be reached.
		addLocal(vendor, "foo", "1.0.0-1")

	man := parseManifest(t, oneProduct+`[dependencies.foo]
source = 'path'
path = 'vendor/foo'
`)

	engine := resolve.NewEngine(fake, projectDir, "tt 3.4.0")

	lock, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.Pins{"foo": "9.9.9-1"})
	require.NoError(t, err)
	assert.Empty(t, warnings)

	got := findDep(t, lock.Products["default"].Dependencies, "foo")
	assert.Equal(t, "path", got.Source)
	assert.Equal(t, "1.0.0-1", got.Version, "the local rockspec decides, not the pin")
	assert.NotEmpty(t, got.ContentHash)
	assert.Zero(t, fake.resolves["foo"], "a path dependency never queries the registry")
}

// TestPinsFromLockExceptFreesOneRock is the `tt package update <name>` scenario
// end to end at the engine level: resolve, let the registry move on, then
// re-resolve holding everything the lock recorded except the one named rock.
func TestPinsFromLockExceptFreesOneRock(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("alpha", "1.0.0-1", "a1").
		add("metrics", "1.0.0-1", "m1")

	man := parseManifest(t, oneProduct+`[dependencies]
alpha = '>=1.0.0'
metrics = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	first, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	require.Equal(t, "1.0.0-1", findDep(t, first.Products["default"].Dependencies, "alpha").Version)

	// Both rocks publish a new version between the two resolutions.
	fake.add("alpha", "2.0.0-1", "a2").add("metrics", "2.0.0-1", "m2")

	pins := resolve.PinsFromLock(first, "metrics")
	assert.Equal(t, resolve.Pins{"alpha": "1.0.0-1"}, pins)

	second, warnings, err := engine.ResolvePinned(context.Background(), man, pins)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	dependencies := second.Products["default"].Dependencies
	assert.Equal(t, "1.0.0-1", findDep(t, dependencies, "alpha").Version,
		"an unnamed dependency must not move")
	assert.Equal(t, "2.0.0-1", findDep(t, dependencies, "metrics").Version,
		"the named dependency must be re-resolved against the registry")
}

func TestPinsFromLockSkipsPathDependencies(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "metrics", Version: "1.0.0-1", Source: "registry"},
				{Name: "vendored", Version: "0.1.0-1", Source: "path", Path: "vendor/x"},
			}},
		},
	}

	assert.Equal(t, resolve.Pins{"metrics": "1.0.0-1"}, resolve.PinsFromLock(lock))
}

// TestPinsFromLockPrefersHighestOnDisagreement documents the tie-break: a Pins
// holds one version per name, so two products that resolved the same rock
// differently have to be reconciled, and the higher version wins rather than
// the first product in sorted order. Taking p1's here would silently downgrade
// p2 below what its own lock entry recorded.
func TestPinsFromLockPrefersHighestOnDisagreement(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		Products: map[string]manifest.LockProduct{
			"p1": {Dependencies: []manifest.LockDependency{
				{Name: "common", Version: "1.5.0-1", Source: "registry"},
			}},
			"p2": {Dependencies: []manifest.LockDependency{
				{Name: "common", Version: "2.0.0-1", Source: "registry"},
			}},
		},
	}

	assert.Equal(t, resolve.Pins{"common": "2.0.0-1"}, resolve.PinsFromLock(lock))
}

// TestPinsFromLockIncludesDevClosure pins that the dev closure is pinned like
// any product closure, under the same three rules: path sources are skipped,
// except is honoured, and the highest version wins where a rock is in both a
// product closure and the dev closure. Without it a tt package add - which
// re-resolves with these pins precisely so unrelated dependencies stay put -
// would drag every dev dependency to its newest version.
func TestPinsFromLockIncludesDevClosure(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "metrics", Version: "1.0.0-1", Source: "registry"},
				{Name: "common", Version: "1.5.0-1", Source: "registry"},
			}},
		},
		DevDependencies: []manifest.LockDependency{
			{Name: "luatest", Version: "1.0.1-1", Source: "registry"},
			{Name: "luacov", Version: "0.15.0-1", Source: "registry"},
			{Name: "dev-helper", Version: "0.1.0", Source: "path", Path: "dev/helper"},
			// Higher than the product's pick of the same rock: the tie-break
			// takes the highest, exactly as it does between two products.
			{Name: "common", Version: "2.0.0-1", Source: "registry"},
		},
	}

	assert.Equal(t, resolve.Pins{
		"metrics": "1.0.0-1",
		"common":  "2.0.0-1",
		"luatest": "1.0.1-1",
		"luacov":  "0.15.0-1",
	}, resolve.PinsFromLock(lock))

	assert.Equal(t, resolve.Pins{
		"metrics": "1.0.0-1",
		"common":  "2.0.0-1",
		"luacov":  "0.15.0-1",
	}, resolve.PinsFromLock(lock, "luatest"),
		"except must free a dev dependency like any other")
}

// TestPinsFromLockHoldsDevDependencyAcrossReresolve is the end-to-end shape of
// the same rule: a dev dependency that published a newer version between two
// resolutions stays where the lock put it.
func TestPinsFromLockHoldsDevDependencyAcrossReresolve(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.0.0-1", "m1").
		add("luatest", "1.0.0-1", "l1")

	man := parseManifest(t, oneProduct+`[dependencies]
metrics = '>=1.0.0'
[dev_dependencies]
luatest = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	first, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	require.Equal(t, "1.0.0-1", findDep(t, first.DevDependencies, "luatest").Version)

	fake.add("luatest", "2.0.0-1", "l2")

	second, warnings, err := engine.ResolvePinned(
		context.Background(), man, resolve.PinsFromLock(first))
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, "1.0.0-1", findDep(t, second.DevDependencies, "luatest").Version,
		"a new registry version must not move a pinned dev dependency")
}

func TestPinsFromLockEdgeCases(t *testing.T) {
	t.Parallel()

	assert.Equal(t, resolve.Pins{}, resolve.PinsFromLock(nil), "a nil lock yields no pins")

	lock := &manifest.Lock{
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "alpha", Version: "1.0.0-1", Source: "registry"},
				{Name: "beta", Version: "2.0.0-1", Source: "registry"},
				{Name: "novers", Version: "", Source: "registry"},
			}},
		},
	}

	assert.Equal(t, resolve.Pins{"beta": "2.0.0-1"},
		resolve.PinsFromLock(lock, "alpha", "absent"),
		"except drops the named rocks and tolerates names not in the lock")
}
