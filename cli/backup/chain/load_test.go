package chain

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/storage"
)

func TestLoadEmptyStorage(t *testing.T) {
	chain, err := Load(t.Context(), newMemoryStorage())
	require.NoError(t, err)
	require.Empty(t, chain.Groups())
	require.Nil(t, chain.Latest())
	require.Empty(t, chain.Manifests())
	require.Empty(t, chain.Problems())
}

func TestLoadBuildsMarkedChain(t *testing.T) {
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	incremental := manifestFixture(
		"incremental", "full", "full", backup.BackupTypeIncremental, 20, 10, 20, nil,
	)
	store.addManifest(t, incremental)
	store.addManifest(t, full)

	chain, err := Load(t.Context(), store)
	require.NoError(t, err)
	require.Len(t, chain.Groups(), 1)
	require.Equal(t,
		[]backup.BackupID{"full", "incremental"},
		entryIDs(chain.Groups()[0].Entries),
	)
	require.Empty(t, chain.Problems())
	require.Equal(t, backup.BackupID("incremental"), chain.Latest().Manifest.BackupID)
}

func TestLoadReturnsMarkedProblems(t *testing.T) {
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	orphan := manifestFixture(
		"orphan", "missing", "full", backup.BackupTypeIncremental, 20, 10, 20, nil,
	)
	store.addManifest(t, full)
	store.addManifest(t, orphan)

	chain, err := Load(t.Context(), store)
	require.NoError(t, err)
	require.Equal(t, ProblemOrphan, chain.Latest().Problems[0].Kind)
	problems := chain.Problems()
	require.Len(t, problems, 1)
	require.Equal(t, ProblemOrphan, problems[0].Kind)
}

func TestLoadRetriesOnVanishedManifest(t *testing.T) {
	// GET misses once (gc race) then succeeds on retry: Load recovers.
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	incremental := manifestFixture(
		"incremental", "full", "full", backup.BackupTypeIncremental, 20, 10, 20, nil,
	)
	store.addManifest(t, full)
	store.addManifest(t, incremental)
	store.getMisses[storage.ManifestKey("incremental")] = 1

	chain, err := Load(t.Context(), store)
	require.NoError(t, err)
	require.Equal(t,
		[]backup.BackupID{"full", "incremental"},
		entryIDs(chain.Groups()[0].Entries),
	)
}

func TestLoadFailsWhenManifestStaysVanished(t *testing.T) {
	// A phantom key is listed but never readable: a second miss is a real error.
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	store.addManifest(t, full)
	store.phantom[storage.ManifestKey("ghost")] = true

	_, err := Load(t.Context(), store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vanished")
}

func TestLoadRejectsInvalidManifest(t *testing.T) {
	store := newMemoryStorage()
	store.objects[storage.ManifestKey("broken")] = []byte("{")

	_, err := Load(t.Context(), store)
	require.Error(t, err)
}

func TestLoadMarksInvalidManifest(t *testing.T) {
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	store.addManifest(t, full)

	invalid := manifestFixture("invalid", "full", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	invalid.SchemaVersion = 0
	store.objects[storage.ManifestKey("invalid")] = encodeManifest(t, invalid)

	chain, err := Load(t.Context(), store)
	require.NoError(t, err)
	require.Contains(t, problemKinds(chain.Problems()), ProblemInvalidManifest)
}

func TestLoadPartialReportsUndecodableManifest(t *testing.T) {
	// A single broken object must not hide the manifests around it.
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	store.addManifest(t, full)
	store.objects[storage.ManifestKey("broken")] = []byte("{")

	chain, unreadable, err := LoadPartial(t.Context(), store)
	require.NoError(t, err)
	require.Equal(t, []backup.BackupID{"full"}, entryIDs(chain.Groups()[0].Entries))
	require.Len(t, unreadable, 1)
	require.Equal(t, storage.ManifestKey("broken"), unreadable[0].Key)
	require.ErrorContains(t, unreadable[0].Err, "decode manifest")
}

func TestLoadPartialReportsDuplicateBackupID(t *testing.T) {
	// A manifest copied to a second key used to make Build reject the storage
	// whole, so verify exited "could not check" and said nothing about the very
	// defect it exists to name. Both copies are reported and distrusted; the
	// backups around them are still checked.
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	other := manifestFixture("other", "", "other", backup.BackupTypeFull, 10, 0, 10, nil)
	store.addManifest(t, full)
	store.addManifest(t, other)
	store.objects[storage.ManifestsPrefix()+"full-copy.json"] =
		store.objects[storage.ManifestKey("full")]

	chain, unreadable, err := LoadPartial(t.Context(), store)

	require.NoError(t, err)
	require.Len(t, unreadable, 2)
	require.Equal(t, storage.ManifestsPrefix()+"full-copy.json", unreadable[0].Key)
	require.Equal(t, storage.ManifestKey("full"), unreadable[1].Key)
	require.ErrorContains(t, unreadable[0].Err, "duplicate backup_id")
	require.Equal(t, []backup.BackupID{"other"}, entryIDs(chain.Groups()[0].Entries))
}

func TestLoadPartialOrdersProblemsDeterministically(t *testing.T) {
	// The loops that attach problems used to run over maps, so an entry
	// inheriting from several broken ancestors collected them in whatever order
	// Go felt like that run. One run cannot catch it - the orderings differ only
	// across runs - so the same storage is built repeatedly and compared.
	build := func() []string {
		store := newMemoryStorage()
		// Two invalid ancestors poisoning the same tail. They must fail in
		// different ways: identical details would make a reordering invisible.
		first := manifestFixture("a-bad", "", "a-bad", backup.BackupTypeFull, 10, 0, 10, nil)
		first.Status = ""
		second := manifestFixture("b-bad", "a-bad", "a-bad",
			backup.BackupTypeIncremental, 20, 10, 20, nil)
		second.SchemaVersion = 99
		tail := manifestFixture("c-tail", "b-bad", "a-bad",
			backup.BackupTypeIncremental, 30, 20, 30, nil)
		store.addManifest(t, first)
		store.addManifest(t, second)
		store.addManifest(t, tail)

		chain, _, err := LoadPartial(t.Context(), store)
		require.NoError(t, err)

		details := make([]string, 0)
		for _, problem := range chain.Problems() {
			details = append(details, fmt.Sprintf("%v|%s|%s",
				problem.Kind, problem.BackupID, problem.Detail))
		}

		return details
	}

	want := build()
	require.NotEmpty(t, want, "the fixture must actually produce problems")

	for range 50 {
		require.Equal(t, want, build(), "problem order must not vary between runs")
	}
}

func TestLoadPartialKeepsManifestsInACycle(t *testing.T) {
	// Two increments pointing at each other are reachable neither from the full
	// backup nor as tail roots. They used to fall out of the ordered group
	// entirely: unchecked, uncounted, and with their archives reported dangling
	// on top - verify blaming the healthy half of a broken pair.
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	first := manifestFixture("a", "b", "full", backup.BackupTypeIncremental, 20, 10, 20, nil)
	second := manifestFixture("b", "a", "full", backup.BackupTypeIncremental, 30, 20, 30, nil)
	store.addManifest(t, full)
	store.addManifest(t, first)
	store.addManifest(t, second)

	chain, unreadable, err := LoadPartial(t.Context(), store)

	require.NoError(t, err)
	require.Empty(t, unreadable)
	require.ElementsMatch(t,
		[]backup.BackupID{"full", "a", "b"},
		entryIDs(chain.Groups()[0].Entries),
		"a manifest no command can see is one gc would delete around",
	)
	require.NotEmpty(t, chain.Problems(), "the cycle must be reported, not hidden")
}

func TestLoadFailsOnDuplicateBackupID(t *testing.T) {
	// Load stays fail-loud: a caller that recovers from the chain must not be
	// handed one of two objects claiming the same backup.
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	store.addManifest(t, full)
	store.objects[storage.ManifestsPrefix()+"full-copy.json"] =
		store.objects[storage.ManifestKey("full")]

	_, err := Load(t.Context(), store)

	require.ErrorContains(t, err, "duplicate backup_id")
}

func TestLoadPartialRetriesOnVanishedManifest(t *testing.T) {
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	store.addManifest(t, full)
	store.getMisses[storage.ManifestKey("full")] = 1

	chain, unreadable, err := LoadPartial(t.Context(), store)
	require.NoError(t, err)
	require.Empty(t, unreadable)
	require.Equal(t, []backup.BackupID{"full"}, entryIDs(chain.Groups()[0].Entries))
}

func TestLoadPartialReportsPhantomManifest(t *testing.T) {
	// Listed but never readable: the retry does not help, so it is reported.
	store := newMemoryStorage()
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	store.addManifest(t, full)
	store.phantom[storage.ManifestKey("ghost")] = true

	chain, unreadable, err := LoadPartial(t.Context(), store)
	require.NoError(t, err)
	require.Len(t, chain.Manifests(), 1)
	require.Len(t, unreadable, 1)
	require.ErrorContains(t, unreadable[0].Err, "vanished")
}

func TestBuildKeepsManifestsInAPreviousBackupIDCycle(t *testing.T) {
	// Two increments pointing at each other are reachable neither from the full
	// backup nor as a tail root. They must still show up in the chain, flagged,
	// rather than vanishing from every diagnostic.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	left := manifestFixture("left", "right", "full", backup.BackupTypeIncremental, 20, 10, 20, nil)
	right := manifestFixture("right", "left", "full", backup.BackupTypeIncremental, 30, 20, 30, nil)

	chain := buildFixtureChain(t, full, left, right)

	require.Len(t, chain.Manifests(), 3)
	require.Contains(t, problemKinds(chain.Problems()), ProblemInvalidManifest)

	for _, entry := range chain.Groups()[0].Entries {
		if entry.Manifest.BackupID == "full" {
			continue
		}

		require.NotEmpty(t, entry.Problems,
			"manifest must be flagged: ", entry.Manifest.BackupID)
	}
}

func TestBuildDoesNotFlagAHealthyChainAsACycle(t *testing.T) {
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	incremental := manifestFixture(
		"incremental", "full", "full", backup.BackupTypeIncremental, 20, 10, 20, nil,
	)

	chain := buildFixtureChain(t, full, incremental)

	require.Empty(t, chain.Problems())
}

func TestLoadReturnsListError(t *testing.T) {
	wantErr := errors.New("list failed")
	store := newMemoryStorage()
	store.listErr = wantErr

	_, err := Load(t.Context(), store)
	require.ErrorIs(t, err, wantErr)
}

func TestLoadReturnsGetError(t *testing.T) {
	wantErr := errors.New("get failed")
	store := newMemoryStorage()
	key := storage.ManifestKey("full")
	store.objects[key] = []byte("{}")
	store.getErr[key] = wantErr

	_, err := Load(t.Context(), store)
	require.ErrorIs(t, err, wantErr)
}
