package chain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/lib/backup"
	"github.com/tarantool/tt/lib/backup/storage"
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
