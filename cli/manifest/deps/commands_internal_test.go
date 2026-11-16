package deps

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
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

// TestAdd_refusesAConstraintNoResolverCanUse pins that the constraint is
// checked before the manifest is opened for editing: a declaration the resolver
// could never act on leaves the file exactly as it was, and no resolve is
// attempted.
func TestAdd_refusesAConstraintNoResolverCanUse(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	_, err := addWith(context.Background(), optsFor(dir), res, "metrics", "~~1.0", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "~~1.0")

	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
	assert.Empty(t, res.pins)
}

// TestAdd_acceptsTheAnyConstraint covers the constraint tt package add writes
// when the user names no version: it reaches the resolver as "no bound at all"
// rather than as a literal the version grammar rejects.
func TestAdd_acceptsTheAnyConstraint(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	_, err := addWith(context.Background(), optsFor(dir), res, "metrics", "*", false)
	require.NoError(t, err)

	assert.Contains(t, manifestOnDisk(t, dir), "metrics = \"*\"")

	declared := res.seen[0].Dependencies["metrics"]
	assert.Equal(t, "*", declared.Version)
	assert.Empty(t, manifest.ConstraintExpr(declared.Version))
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

// TestAdd_resolveFailureLeavesBothFilesUnchanged covers the all-or-nothing
// rule: a resolution that fails, whatever for, writes neither file, and the
// error says the manifest was left unchanged, so the next command, a build
// say, works on the project as it was.
func TestAdd_resolveFailureLeavesBothFilesUnchanged(t *testing.T) {
	t.Parallel()

	locks := []*manifest.Lock{nil, lockOf(map[string]string{"checks": "3.1.0-1"})}

	for _, failure := range []struct {
		name string
		err  error
	}{
		{"unknown rock", errors.New(`"metrics": rock not found on any server`)},
		{"no version satisfies", errors.New(`no version of "metrics" satisfies >=9.0.0`)},
		{"network", errors.New("dial tcp 127.0.0.1:1: connection refused")},
		{"interrupted", context.Canceled},
	} {
		t.Run(failure.name, func(t *testing.T) {
			t.Parallel()

			for _, before := range locks {
				dir := writeProject(t, baseManifest, before)
				lockBefore := lockBytes(t, dir)
				res := &fakeResolver{err: failure.err}

				_, err := addWith(context.Background(), optsFor(dir), res,
					"metrics", ">=1.0.0", false)
				require.ErrorIs(t, err, failure.err)
				require.ErrorIs(t, err, ErrManifestUnchanged)
				require.NotErrorIs(t, err, ErrManifestEdited)
				assert.Contains(t, err.Error(), "the manifest was left unchanged")
				assert.Equal(t, 1, exitcode.Code(err))

				assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
				assert.Equal(t, lockBefore, lockBytes(t, dir))
			}
		})
	}
}

// TestAddFromRegistry_resolveFailureLeavesBothFilesUnchanged is the same rule
// for the long form an SDK module's AddDependency writes.
func TestAddFromRegistry_resolveFailureLeavesBothFilesUnchanged(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	lockBefore := lockBytes(t, dir)
	boom := errors.New("rock not found on any server")

	_, err := addFromRegistryWith(context.Background(), optsFor(dir), &fakeResolver{err: boom},
		"metrics", ">=1.0.0", "https://rocks.example.org", false)
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, err, ErrManifestUnchanged)

	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
	assert.Equal(t, lockBefore, lockBytes(t, dir))
}

// TestRemove_resolveFailureLeavesBothFilesUnchanged: remove edits the
// manifest before it resolves as add does, and a failed resolution - a server
// that did not answer for what remains, say - keeps the declaration.
func TestRemove_resolveFailureLeavesBothFilesUnchanged(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	lockBefore := lockBytes(t, dir)
	boom := errors.New("connection refused")

	_, err := removeWith(context.Background(), optsFor(dir), &fakeResolver{err: boom}, "checks")
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, err, ErrManifestUnchanged)
	assert.Equal(t, 1, exitcode.Code(err))

	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
	assert.Equal(t, lockBefore, lockBytes(t, dir))
}

// TestUpdate_resolveFailureSaysNothingOfTheManifest: update edits no
// declaration, so its failure is the resolution's alone, the lock untouched.
func TestUpdate_resolveFailureSaysNothingOfTheManifest(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	lockBefore := lockBytes(t, dir)
	boom := errors.New("connection refused")

	_, err := updateWith(context.Background(), optsFor(dir), &fakeResolver{err: boom}, "")
	require.ErrorIs(t, err, boom)
	require.NotErrorIs(t, err, ErrManifestUnchanged)
	require.NotErrorIs(t, err, ErrManifestEdited)

	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
	assert.Equal(t, lockBefore, lockBytes(t, dir))
}

// TestAdd_lockWriteFailurePutsTheManifestBack covers the one write that comes
// after another: the manifest is written, then the lock, and a lock that
// cannot be written - here its path is a link into a directory that is not
// there - has the manifest put back as it was, and no lock left behind.
func TestAdd_lockWriteFailurePutsTheManifestBack(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing", lockFileName),
		filepath.Join(dir, lockFileName)))

	_, err := addWith(context.Background(), optsFor(dir), &fakeResolver{},
		"metrics", ">=1.0.0", false)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.ErrorIs(t, err, ErrManifestUnchanged)
	require.NotErrorIs(t, err, ErrManifestEdited)
	assert.Contains(t, err.Error(), "writing "+lockFileName)

	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))

	_, err = os.Stat(filepath.Join(dir, lockFileName))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// TestAdd_readOnlyLockIsLeftAsItWas: a lock the user may not write refuses
// the write before changing it, so there is nothing of it to put back, and
// the manifest written before it is put back.
func TestAdd_readOnlyLockIsLeftAsItWas(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	lockBefore := lockBytes(t, dir)
	require.NoError(t, os.Chmod(filepath.Join(dir, lockFileName), 0o400))

	_, err := addWith(context.Background(), optsFor(dir), &fakeResolver{},
		"metrics", ">=1.0.0", false)
	require.ErrorIs(t, err, fs.ErrPermission)
	require.ErrorIs(t, err, ErrManifestUnchanged)
	assert.NotContains(t, err.Error(), "putting")

	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
	assert.Equal(t, lockBefore, lockBytes(t, dir))
}

// TestRestore_reportsAManifestItCouldNotPutBack: when the manifest cannot be
// put back either, the files disagree and the error says so.
func TestRestore_reportsAManifestItCouldNotPutBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A directory where the manifest was: nothing can be written over it.
	require.NoError(t, os.Mkdir(filepath.Join(dir, manifestFileName), 0o700))

	proj := &project{source: []byte(baseManifest), perm: 0o600}
	boom := errors.New("disk full")

	err := restore(optsFor(dir), proj, []byte("edited"), true, boom)
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, err, ErrManifestEdited)
	require.NotErrorIs(t, err, ErrManifestUnchanged)
}

// TestPutBack covers the three states a file is put back from: written over,
// created where there was none, and refused before it changed.
func TestPutBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file")

	// Written over: the bytes it had are written back.
	require.NoError(t, os.WriteFile(path, []byte("new"), 0o600))
	require.NoError(t, putBack(path, []byte("old"), 0o600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "old", string(data))

	// Created where there was none: it is removed, and putting back a file
	// that is not there is not an error.
	require.NoError(t, putBack(path, nil, 0o600))

	_, err = os.Stat(path)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, putBack(path, nil, 0o600))

	// Refused before it changed: a file that is the source already is not
	// written, so a read-only one is no error.
	if os.Geteuid() != 0 {
		require.NoError(t, os.WriteFile(path, []byte("old"), 0o400))
		require.NoError(t, putBack(path, []byte("old"), 0o600))
	}
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
	assert.Equal(t, 1, exitcode.Code(err))
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
	assert.Equal(t, 1, exitcode.Code(err))
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

// TestDeclare_keepsADeclarationGivenNoConstraint: tt package add of a name
// declared already, with no constraint, leaves the declaration as it is
// instead of loosening it to "*", and with a current lock does nothing.
func TestDeclare_keepsADeclarationGivenNoConstraint(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	// The first lock: a project with none is resolved, the declaration kept.
	result, err := declareWith(context.Background(), optsFor(dir), res, "checks", "", false)
	require.NoError(t, err)
	assert.True(t, result.Kept)
	assert.True(t, result.Change.Existed)
	assert.Equal(t, ">=3.0.0", result.Change.Previous)
	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
	require.Len(t, res.pins, 1)

	lock := lockBytes(t, dir)
	require.NotNil(t, lock)

	// A current lock: a no-op, nothing resolved and nothing written.
	result, err = declareWith(context.Background(), optsFor(dir), res, "checks", "", false)
	require.NoError(t, err)
	assert.True(t, result.Kept)
	assert.Equal(t, ">=3.0.0", result.Change.Previous)
	assert.Empty(t, result.Moves)
	assert.Empty(t, result.Rewrites)
	assert.Len(t, res.pins, 1, "a current lock is not resolved again")
	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
	assert.Equal(t, lock, lockBytes(t, dir))
}

// TestDeclare_resolvesAStaleLock: a kept declaration still brings a stale
// lock in step, holding the versions it has.
func TestDeclare_resolvesAStaleLock(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, lockOf(map[string]string{"checks": "3.1.0-1"}))
	res := &fakeResolver{}

	result, err := declareWith(context.Background(), optsFor(dir), res, "checks", "", false)
	require.NoError(t, err)
	assert.True(t, result.Kept)
	require.Len(t, res.pins, 1)
	assert.Equal(t, resolve.Pins{"checks": "3.1.0-1"}, res.pins[0])
	assert.Equal(t, baseManifest, manifestOnDisk(t, dir))
}

// TestDeclare_constraintStillRewrites: an explicit constraint rewrites the
// declaration, and a name new to the table is declared as "*".
func TestDeclare_constraintStillRewrites(t *testing.T) {
	t.Parallel()

	dir := writeProject(t, baseManifest, nil)
	res := &fakeResolver{}

	result, err := declareWith(context.Background(), optsFor(dir), res, "checks", ">=3.2.0", false)
	require.NoError(t, err)
	assert.False(t, result.Kept)
	assert.Equal(t, ">=3.0.0", result.Change.Previous)
	assert.Contains(t, manifestOnDisk(t, dir), `checks = ">=3.2.0"`)

	// checks is declared in [dependencies], not in [dev_dependencies]: an add
	// to the dev table is a new declaration there.
	result, err = declareWith(context.Background(), optsFor(dir), res, "checks", "", true)
	require.NoError(t, err)
	assert.False(t, result.Kept)
	assert.False(t, result.Change.Existed)
	assert.Equal(t, "*", result.Manifest.DevDependencies["checks"].Version)
}

// bothTablesManifest declares luatest as a runtime and as a dev dependency.
const bothTablesManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
checks = '>=3.0.0'
luatest = '>=1.0.0'

[dev_dependencies]
luatest = '>=1.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
`

// TestRemoveFrom_touchesOneTable: tt package remove drops the runtime
// declaration only, --dev the dev one only, the other kept with a warning.
func TestRemoveFrom_touchesOneTable(t *testing.T) {
	t.Parallel()

	for _, dev := range []bool{false, true} {
		dir := writeProject(t, bothTablesManifest, nil)
		res := &fakeResolver{}

		var warnings []string

		opts := optsFor(dir)

		opts.Warn = func(msg string) { warnings = append(warnings, msg) }

		result, err := removeFromWith(context.Background(), opts, res, "luatest", dev)
		require.NoError(t, err)

		if dev {
			assert.Equal(t, ">=1.1.0", result.Change.Previous)
			assert.Contains(t, result.Manifest.Dependencies, "luatest")
			assert.NotContains(t, result.Manifest.DevDependencies, "luatest")
			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0], "[dependencies]")
		} else {
			assert.Equal(t, ">=1.0.0", result.Change.Previous)
			assert.NotContains(t, result.Manifest.Dependencies, "luatest")
			assert.Contains(t, result.Manifest.DevDependencies, "luatest")
			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0], "[dev_dependencies]")
		}
	}
}

// TestRemoveFrom_otherTableSuggestsTheFlag: a name only the other table
// declares is refused, nothing written, and the error says how to remove it.
func TestRemoveFrom_otherTableSuggestsTheFlag(t *testing.T) {
	t.Parallel()

	const devOnly = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dev_dependencies]
luatest = '>=1.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
`

	dir := writeProject(t, devOnly, nil)
	res := &fakeResolver{}

	_, err := removeFromWith(context.Background(), optsFor(dir), res, "luatest", false)
	require.ErrorIs(t, err, ErrNotDeclared)
	assert.Equal(t, 1, exitcode.Code(err))
	assert.Contains(t, err.Error(), "not declared in [dependencies] but in [dev_dependencies]")
	assert.Contains(t, err.Error(), "tt package remove --dev luatest")
	assert.Empty(t, res.pins)
	assert.Equal(t, devOnly, manifestOnDisk(t, dir))

	dir = writeProject(t, baseManifest, nil)

	_, err = removeFromWith(context.Background(), optsFor(dir), res, "checks", true)
	require.ErrorIs(t, err, ErrNotDeclared)
	assert.Contains(t, err.Error(), "tt package remove checks")
	assert.NotContains(t, err.Error(), "--dev")
}
