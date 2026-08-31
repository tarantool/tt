package deps

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
)

// TestAdd_pinsEveryLockedVersion is the behavioural core of add: the rock being
// added resolves fresh, every other rock is held at the version the lock
// already chose. Without the pins an edit to one dependency would drag every
// unrelated one forward.
func TestAdd_pinsEveryLockedVersion(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{
		"checks":    "3.1.0-1",
		"luasocket": "3.0.0-1",
	}))
	res := &fakeResolver{}

	_, err := addWith(context.Background(), optsFor(dir), res, "metrics", ">=1.0.0", false)
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Equal(t, resolve.Pins{"checks": "3.1.0-1", "luasocket": "3.0.0-1"}, res.pins[0])
}

// TestAdd_noLockPinsNothing covers a project that has never resolved: there is
// nothing to hold, and that is a normal state rather than an error.
func TestAdd_noLockPinsNothing(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	_, err := addWith(context.Background(), optsFor(dir), res, "metrics", "", false)
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Empty(t, res.pins[0])
	assert.Contains(t, manifestOnDisk(t, dir), "metrics = \"*\"")
}

// TestAdd_lockRecordsTheEditedManifestHash pins the invariant the whole
// read-edit-write-reparse-resolve order exists for. manifest_hash is taken over
// the raw source bytes a Manifest was parsed from, so resolving the pre-edit
// model stamps the lock with the pre-edit hash and IsStale then reports the
// freshly written lock as stale on the very next command.
func TestAdd_lockRecordsTheEditedManifestHash(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	res := &fakeResolver{}

	result, err := addWith(context.Background(), optsFor(dir), res, "metrics", ">=1.0.0", false)
	require.NoError(t, err)

	edited, warnings, err := manifest.ParseManifest([]byte(manifestOnDisk(t, dir)))
	require.NoError(t, err)
	require.Empty(t, warnings)

	assert.Equal(t, edited.Hash(), result.Lock.ManifestHash)
	assert.Equal(t, edited.Hash(), lockOnDisk(t, dir).ManifestHash)

	// The engine only needs its project directory to answer this; the manifest
	// declares no path dependency, so no adapter call can happen.
	stale, reason, err := resolve.NewEngine(nil, dir, "tt test").
		IsStale(edited, lockOnDisk(t, dir))
	require.NoError(t, err)
	assert.False(t, stale, reason)
}

// TestAdd_rewritesAnExistingConstraint covers a name the table already declares:
// the constraint is replaced, the previous one is reported, and the comment
// above the table is untouched.
func TestAdd_rewritesAnExistingConstraint(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	result, err := addWith(context.Background(), optsFor(dir), res, "checks", ">=3.2.0", false)
	require.NoError(t, err)

	assert.True(t, result.Change.Existed)
	assert.Equal(t, ">=3.0.0", result.Change.Previous)

	source := manifestOnDisk(t, dir)
	assert.Contains(t, source, `checks = ">=3.2.0"`)
	assert.NotContains(t, source, "checks = '>=3.0.0'")
	assert.Contains(t, source, "# checks validates the config; do not drop it.")
}

// TestAdd_devWritesTheDevTable covers --dev: the entry lands in
// [dev_dependencies], and [dependencies] is left alone.
func TestAdd_devWritesTheDevTable(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	_, err := addWith(context.Background(), optsFor(dir), res, "luatest", ">=1.0.0", true)
	require.NoError(t, err)

	edited, _, err := manifest.ParseManifest([]byte(manifestOnDisk(t, dir)))
	require.NoError(t, err)

	assert.Contains(t, edited.DevDependencies, "luatest")
	assert.NotContains(t, edited.Dependencies, "luatest")
	assert.Contains(t, edited.Dependencies, "checks")
}

// TestAdd_resolveFailureLeavesTheManifestEdited covers the documented order: the
// requested edit reaches disk first, so a failed resolution leaves the two files
// disagreeing — and the error has to say so, because every later command will
// fail the same way until the manifest is fixed.
func TestAdd_resolveFailureLeavesTheManifestEdited(t *testing.T) {
	t.Parallel()

	before := lockOf(map[string]string{"checks": "3.1.0-1"})
	dir := writeProject(t, baseManifest, before)
	boom := errors.New("no server has it")
	res := &fakeResolver{err: boom}

	_, err := addWith(context.Background(), optsFor(dir), res, "metrics", ">=1.0.0", false)
	require.Error(t, err)
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, err, ErrManifestEdited)
	assert.Equal(t, 1, ExitCode(err))

	assert.Contains(t, manifestOnDisk(t, dir), "metrics")
	// The lock is exactly the one that was there: nothing rewrote it.
	assert.Equal(t, "sha256:stale", lockOnDisk(t, dir).ManifestHash)
}

// TestAdd_reportsMoves checks the closure diff the CLI prints: a rock that
// changed version, one that arrived, and one that dropped out. A rock that did
// not move is not reported.
func TestAdd_reportsMoves(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{
		"checks":    "3.1.0-1",
		"luasocket": "3.0.0-1",
		"gone":      "0.1.0-1",
	}))
	res := &fakeResolver{lock: lockOf(map[string]string{
		"checks":    "3.2.0-1",
		"luasocket": "3.0.0-1",
		"metrics":   "1.0.0-1",
	})}

	result, err := addWith(context.Background(), optsFor(dir), res, "metrics", ">=1.0.0", false)
	require.NoError(t, err)

	assert.Equal(t, []Move{
		{Name: "checks", From: "3.1.0-1", To: "3.2.0-1"},
		{Name: "gone", From: "0.1.0-1", To: ""},
		{Name: "metrics", From: "", To: "1.0.0-1"},
	}, result.Moves)
}

// TestRemove_dropsTheEntryAndPinsTheRest covers remove's own pin set — every
// remaining rock is held — and that the declaration is gone from the file while
// the comment above the table stays.
func TestRemove_dropsTheEntryAndPinsTheRest(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{
		"checks":    "3.1.0-1",
		"luasocket": "3.0.0-1",
	}))
	res := &fakeResolver{}

	result, err := removeWith(context.Background(), optsFor(dir), res, "checks")
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Equal(t, resolve.Pins{"checks": "3.1.0-1", "luasocket": "3.0.0-1"}, res.pins[0])

	assert.True(t, result.Change.Existed)
	assert.Equal(t, ">=3.0.0", result.Change.Previous)

	source := manifestOnDisk(t, dir)
	assert.NotContains(t, source, "checks = ")
	assert.Contains(t, source, "# checks validates the config; do not drop it.")
}

// TestRemove_unknownNameIsAnError pins the decided behaviour: a name the
// manifest does not declare is exit 1, not a silent success. Nothing is written
// and the resolver is never reached.
func TestRemove_unknownNameIsAnError(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	_, err := removeWith(context.Background(), optsFor(dir), res, "nosuchrock")
	require.ErrorIs(t, err, ErrNotDeclared)
	assert.Equal(t, 1, ExitCode(err))
	assert.Empty(t, res.pins)
	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
}

// TestUpdate_allPinsNothing is the one command that pulls newer registry
// versions: no pins at all. Everything else in this package exists to make sure
// no other command does that.
func TestUpdate_allPinsNothing(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	res := &fakeResolver{}

	_, err := updateWith(context.Background(), optsFor(dir), res, "")
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Nil(t, res.pins[0])
	// No declaration changed, so the manifest is byte-for-byte what it was.
	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
}

// TestUpdate_oneNameFreesOnlyThatRock covers the targeted form: the named rock
// is left out of the pin set and every other one is held.
func TestUpdate_oneNameFreesOnlyThatRock(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{
		"checks":    "3.1.0-1",
		"luasocket": "3.0.0-1",
	}))
	res := &fakeResolver{}

	_, err := updateWith(context.Background(), optsFor(dir), res, "checks")
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Equal(t, resolve.Pins{"luasocket": "3.0.0-1"}, res.pins[0])
}

// TestUpdate_unknownNameIsAnError mirrors remove: a name the manifest does not
// declare is refused rather than quietly re-resolving everything.
func TestUpdate_unknownNameIsAnError(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	res := &fakeResolver{}

	_, err := updateWith(context.Background(), optsFor(dir), res, "nosuchrock")
	require.ErrorIs(t, err, ErrNotDeclared)
	assert.Equal(t, 1, ExitCode(err))
	assert.Empty(t, res.pins)
}

// componentManifest declares its only dependency inside a component, which is a
// table neither add nor remove may edit.
const componentManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'

[components.lua.dependencies]
checks = '>=3.0.0'
`

// TestRemove_componentOnlyDeclarationIsRefused: which component owns a
// dependency is the user's decision, so remove refuses rather than guessing —
// and says where the declaration actually is.
func TestRemove_componentOnlyDeclarationIsRefused(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, componentManifest, nil)
	res := &fakeResolver{}

	_, err := removeWith(context.Background(), optsFor(dir), res, "checks")
	require.ErrorIs(t, err, ErrNotDeclared)
	assert.Contains(t, err.Error(), "[components.lua.dependencies]")
	assert.Empty(t, res.pins)
}

// TestUpdate_acceptsAComponentDeclaration: update edits nothing, so a
// component's dependency is as updatable as a document-level one.
func TestUpdate_acceptsAComponentDeclaration(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, componentManifest, lockOf(map[string]string{
		"checks":    "3.1.0-1",
		"luasocket": "3.0.0-1",
	}))
	res := &fakeResolver{}

	_, err := updateWith(context.Background(), optsFor(dir), res, "checks")
	require.NoError(t, err)

	require.Len(t, res.pins, 1)
	assert.Equal(t, resolve.Pins{"luasocket": "3.0.0-1"}, res.pins[0])
}

// TestAdd_warnsAboutTheOtherTable: a rock declared as both a runtime and a dev
// dependency is legal TOML and almost always a mistake, so the add says so
// instead of leaving the stale declaration behind silently.
func TestAdd_warnsAboutTheOtherTable(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	var warnings []string

	opts := optsFor(dir)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	_, err := addWith(context.Background(), opts, res, "checks", ">=3.0.0", true)
	require.NoError(t, err)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "[dependencies]")
}
