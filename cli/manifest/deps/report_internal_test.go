package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// mergedManifest declares the same rock twice — globally and in the one
// component the product builds — plus a dev dependency. It is what the report
// has to merge: one entry, both constraints, both places.
const mergedManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
checks = '>=3.0.0'

[dev_dependencies]
luatest = '*'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'

[components.lua.dependencies]
checks = '<4.0.0'
metrics = '>=1.0.0'
`

// bareManifest declares a dependency and no product at all. Nothing resolves it
// into a closure, and the report still has to show it.
const bareManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
checks = '>=3.0.0'
`

// currentLock builds a lock whose hash matches source, so the report reads it
// as current rather than stale. The engine stamps that hash on every lock it
// writes; a fixture that skips it tests the stale path by accident.
func currentLock(
	t *testing.T, source string, pins map[string]string, dev map[string]string,
) *manifest.Lock {
	t.Helper()

	man, warnings, err := manifest.ParseManifest([]byte(source))
	require.NoError(t, err)
	require.Empty(t, warnings)

	lock := lockOf(pins)

	lock.ManifestHash = man.Hash()
	lock.DevDependencies = lockOf(dev).Products["default"].Dependencies

	return lock
}

// entryByName finds one entry of a rendered group.
func entryByName(t *testing.T, entries []Entry, name string) Entry {
	t.Helper()

	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}

	require.Failf(t, "no such entry", "%q is not in the report", name)

	return Entry{}
}

// TestDeps_reportsDeclaredAndLockedVersions is the core of the command: a
// declared rock carries its constraint and the locked version, and a rock only
// the closure holds is reported as indirect.
func TestDeps_reportsDeclaredAndLockedVersions(t *testing.T) {
	t.Parallel()

	lock := currentLock(t, baseManifest,
		map[string]string{"checks": "3.1.0-1", "luasocket": "3.0.0-1"}, nil)
	dir := writeProject(t, baseManifest, lock)

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	assert.Equal(t, "my-app", report.Package)
	assert.Equal(t, LockCurrent, report.Lock)
	assert.Empty(t, report.LockReason)
	require.Len(t, report.Products, 1)
	assert.Equal(t, "default", report.Products[0].Name)

	entries := report.Products[0].Dependencies
	require.Len(t, entries, 2)

	// Declared first, then whatever came in behind it.
	assert.Equal(t, Entry{
		Name: "checks", Constraint: ">=3.0.0", Version: "3.1.0-1", Source: "registry",
		Direct: true, DeclaredIn: []string{"[dependencies]"},
	}, entries[0])
	assert.Equal(t, Entry{
		Name: "luasocket", Version: "3.0.0-1", Source: "registry", Direct: false,
	}, entries[1])
}

// TestDeps_mergesComponentDeclarations covers a rock declared both globally and
// by a component of the product: one entry, the constraints AND-ed the way the
// resolver ANDs them, and both places named.
func TestDeps_mergesComponentDeclarations(t *testing.T) {
	t.Parallel()

	lock := currentLock(t, mergedManifest,
		map[string]string{"checks": "3.1.0-1", "metrics": "1.0.0-1"},
		map[string]string{"luatest": "1.0.1-1"})
	dir := writeProject(t, mergedManifest, lock)

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	require.Len(t, report.Products, 1)

	checks := entryByName(t, report.Products[0].Dependencies, "checks")

	assert.Equal(t, ">=3.0.0,<4.0.0", checks.Constraint)
	assert.Equal(t, []string{"[dependencies]", "[components.lua.dependencies]"}, checks.DeclaredIn)
	assert.True(t, checks.Direct)

	metrics := entryByName(t, report.Products[0].Dependencies, "metrics")
	assert.Equal(t, []string{"[components.lua.dependencies]"}, metrics.DeclaredIn)
}

// TestDeps_reportsTheDevClosure pins that dev dependencies are reported, and
// reported apart from the products: the manifest declares them globally, so
// there is no product to file them under.
func TestDeps_reportsTheDevClosure(t *testing.T) {
	t.Parallel()

	lock := currentLock(t, mergedManifest,
		map[string]string{"checks": "3.1.0-1"},
		map[string]string{"luatest": "1.0.1-1", "checks": "3.1.0-1"})
	dir := writeProject(t, mergedManifest, lock)

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	require.Len(t, report.DevDependencies, 2)

	luatest := entryByName(t, report.DevDependencies, "luatest")
	assert.Equal(t, "1.0.1-1", luatest.Version)
	assert.True(t, luatest.Direct)
	assert.Equal(t, []string{"[dev_dependencies]"}, luatest.DeclaredIn)

	// checks is in the dev closure because the runtime side pinned it there; it
	// is not declared as a dev dependency and must not be reported as one.
	assert.False(t, entryByName(t, report.DevDependencies, "checks").Direct)
}

// TestDeps_missingLockReportsDeclarationsWithoutVersions covers a project that
// has never resolved. Dropping the declarations there would answer "you depend
// on nothing" over a populated [dependencies] table.
func TestDeps_missingLockReportsDeclarationsWithoutVersions(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	assert.Equal(t, LockMissing, report.Lock)
	require.Len(t, report.Products, 1)
	require.Len(t, report.Products[0].Dependencies, 1)

	checks := report.Products[0].Dependencies[0]
	assert.Equal(t, "checks", checks.Name)
	assert.Equal(t, ">=3.0.0", checks.Constraint)
	assert.Empty(t, checks.Version)
	assert.True(t, checks.Direct)
}

// scopedManifest declares one dependency in the long form, pinning both the
// source and a registry override so the report has something to carry that the
// lock cannot supply.
const scopedManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies.checks]
version = '>=3.0.0'
source = 'registry'
registry = 'https://rocks.example.org'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
`

// TestDeps_declaredSourceSurvivesAMissingLock: before the first resolve there
// is no locked entry to read a source from, and the declaration is the only
// thing that knows one. Reporting the lock's empty source instead would blank
// the column for every dependency of an unresolved project.
func TestDeps_declaredSourceSurvivesAMissingLock(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, scopedManifest, nil)

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	require.Len(t, report.Products, 1)
	require.Len(t, report.Products[0].Dependencies, 1)

	checks := report.Products[0].Dependencies[0]
	assert.Empty(t, checks.Version)
	assert.Equal(t, "registry", checks.Source)
}

// TestDeps_reportsTheDeclaredRegistry: a dependency pointed at a registry other
// than the default resolves against that one, so a report that omits it cannot
// explain where a version came from.
func TestDeps_reportsTheDeclaredRegistry(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, scopedManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	require.Len(t, report.Products, 1)
	require.Len(t, report.Products[0].Dependencies, 1)

	assert.Equal(t, "https://rocks.example.org", report.Products[0].Dependencies[0].Registry)
}

// TestDeps_staleLockIsReportedNotHidden is the honesty requirement: the command
// resolves nothing, so a lock the manifest has moved away from is labelled
// rather than presented as the current answer.
func TestDeps_staleLockIsReportedNotHidden(t *testing.T) {
	t.Parallel()

	// lockOf stamps a hash that no manifest can have.
	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.0.0-1"}))

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	assert.Equal(t, LockStale, report.Lock)
	assert.Equal(t, "manifest changed since the lock was written", report.LockReason)
	// The stale versions are still shown: they are what a build would use until
	// the next resolution, and the label says exactly what they are worth.
	assert.Equal(t, "3.0.0-1",
		entryByName(t, report.Products[0].Dependencies, "checks").Version)
}

// TestDeps_manifestWithoutProductsStillReportsDeclarations covers the skeleton
// tt new writes: dependencies declared, no product to resolve them into.
func TestDeps_manifestWithoutProductsStillReportsDeclarations(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, bareManifest, nil)

	report, err := Deps(optsFor(dir))
	require.NoError(t, err)

	require.Len(t, report.Products, 1)
	assert.Empty(t, report.Products[0].Name)
	require.Len(t, report.Products[0].Dependencies, 1)
	assert.Equal(t, "checks", report.Products[0].Dependencies[0].Name)
}

// TestDeps_missingManifestIsAnError guards the one state that is not a state at
// all: a directory that holds no package.
func TestDeps_missingManifestIsAnError(t *testing.T) {
	t.Parallel()

	report, err := Deps(optsFor(t.TempDir()))
	require.Error(t, err)
	assert.Nil(t, report)
	assert.Equal(t, exitStateError, ExitCode(err))
}
