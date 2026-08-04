package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup/storage"
)

// mockStorage is an in-memory storage.Storage implementation for testing
// Upload, PrepareArchives and rollback behaviour.
type mockStorage struct {
	objects   map[string][]byte
	putErr    func(key string) error // if non-nil, called on Put to decide failure
	deleted   []string               // tracks keys passed to Delete (for rollback assertions)
	deleteErr func(key string) error
}

func newMockStorage() *mockStorage {
	return &mockStorage{objects: make(map[string][]byte)}
}

func (s *mockStorage) List(context.Context, string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

func (s *mockStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrKeyNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *mockStorage) Put(_ context.Context, key string, r io.Reader, size int64) error {
	if s.putErr != nil {
		if err := s.putErr(key); err != nil {
			return fmt.Errorf("put %q: %w", key, err)
		}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if int64(len(data)) != size {
		return fmt.Errorf("size mismatch: got %d, want %d", len(data), size)
	}
	s.objects[key] = data
	return nil
}

func (s *mockStorage) Delete(_ context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	if s.deleteErr != nil {
		return fmt.Errorf("delete %q: %w", key, s.deleteErr(key))
	}
	delete(s.objects, key)
	return nil
}

func (s *mockStorage) has(key string) bool {
	_, ok := s.objects[key]
	return ok
}

// writeArchiveFile creates a .tar.zst archive with the given content and
// returns its path. The filename follows the <backup-id>-<replicaset_uuid>.tar.zst
// convention so that uuidFromArchivePath can extract the UUID.
func writeArchiveFile(t *testing.T, dir, backupID, replicasetUUID string, content []byte) string {
	t.Helper()
	name := fmt.Sprintf("%s-%s.tar.zst", backupID, replicasetUUID)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, content, 0o644))
	return path
}

// The archive that reaches the manager host is not the file the node packed
// until the two are compared: a copy that went wrong in between is otherwise
// published as healthy and found out at restore time.
func TestVerifyArchives(t *testing.T) {
	dir := t.TempDir()
	backupID := BackupID("bid")
	content := []byte("archive payload")

	path := writeArchiveFile(t, dir, string(backupID), testRSA, content)
	sum := sha256.Sum256(content)
	checksum := hex.EncodeToString(sum[:])

	archives, _, err := PrepareArchives([]string{path}, backupID)
	require.NoError(t, err)

	fragmentWith := func(checksum string) []*Fragment {
		fragment := testFragment(testRSA)
		fragment.ChecksumSHA256 = checksum

		return []*Fragment{&fragment}
	}

	t.Run("matching checksum", func(t *testing.T) {
		unverified, err := VerifyArchives(archives, fragmentWith(checksum))
		require.NoError(t, err)
		assert.Empty(t, unverified)
	})

	t.Run("checksums are hex, case must not decide", func(t *testing.T) {
		_, err := VerifyArchives(archives, fragmentWith(strings.ToUpper(checksum)))
		require.NoError(t, err)
	})

	t.Run("the archive changed after it was packed", func(t *testing.T) {
		_, err := VerifyArchives(archives, fragmentWith(strings.Repeat("0", 64)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the archive changed after it was packed")
		assert.Contains(t, err.Error(), checksum)
	})

	t.Run("a fragment without a checksum takes the computed one", func(t *testing.T) {
		fragments := fragmentWith("")

		unverified, err := VerifyArchives(archives, fragments)
		require.NoError(t, err)

		assert.Equal(t, []string{testRSA}, unverified,
			"nothing was verified for that shard, and the caller has to know")
		assert.Equal(t, checksum, fragments[0].ChecksumSHA256,
			"the manifest still has to describe the archive")
	})

	t.Run("an archive no fragment describes", func(t *testing.T) {
		other := testFragment(testRSB)
		other.ChecksumSHA256 = checksum

		_, err := VerifyArchives(archives, []*Fragment{&other})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no fragment describes archive")
	})

	t.Run("the manifest records the checksum computed here", func(t *testing.T) {
		fragments := fragmentWith(strings.ToUpper(checksum))

		_, err := VerifyArchives(archives, fragments)
		require.NoError(t, err)
		assert.Equal(t, checksum, fragments[0].ChecksumSHA256,
			"the stored checksum is the canonical form of what this host read")
	})

	t.Run("a missing archive is a failure, not a pass", func(t *testing.T) {
		absent := []ArchiveToUpload{{
			LocalPath:      filepath.Join(dir, "gone.tar.zst"),
			ReplicasetUUID: testRSA,
		}}

		_, err := VerifyArchives(absent, fragmentWith(checksum))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum archive")
	})
}

// writeFragmentFile writes a valid fragment JSON to a temporary file.
func writeFragmentFile(t *testing.T, dir, name string, fragment Fragment) string {
	t.Helper()
	data, err := json.Marshal(fragment)
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

// testFragment returns a minimal valid Fragment for the given replicaset.
func testFragment(replicasetUUID string) Fragment {
	return Fragment{
		ReplicasetUUID: replicasetUUID,
		InstanceUUID:   "inst-" + replicasetUUID,
		InstanceName:   "master-001",
		Hostname:       "host-" + replicasetUUID,
		Type:           BackupTypeFull,
		VclockEnd:      Vclock{1: 100},
		Files:          []string{"00000000000000000100.xlog"},
		ChecksumSHA256: "abc123",
	}
}

func TestUpload(t *testing.T) {
	ctx := context.Background()
	backupID := BackupID("bid")
	manifestData := []byte(`{"backup_id":"bid"}`)
	manifestKey := storage.ManifestKey(string(backupID))

	dir := t.TempDir()
	archiveA := writeArchiveFile(t, dir, string(backupID), testRSA, []byte("archive-a"))
	archiveB := writeArchiveFile(t, dir, string(backupID), testRSB, []byte("archive-b"))
	keyA := "data/bid-" + testRSA + ".tar.zst"
	keyB := "data/bid-" + testRSB + ".tar.zst"

	twoArchives := []ArchiveToUpload{
		{LocalPath: archiveA, StorageKey: keyA, Size: 9, ReplicasetUUID: testRSA},
		{LocalPath: archiveB, StorageKey: keyB, Size: 9, ReplicasetUUID: testRSB},
	}

	t.Run("success", func(t *testing.T) {
		store := newMockStorage()
		err := Upload(ctx, store, backupID, manifestData, twoArchives)
		require.NoError(t, err)

		assert.True(t, store.has(keyA))
		assert.True(t, store.has(keyB))
		assert.True(t, store.has(manifestKey))
		assert.Equal(t, manifestData, store.objects[manifestKey])
		assert.Empty(t, store.deleted)
	})

	t.Run("manifest only, no archives", func(t *testing.T) {
		store := newMockStorage()
		err := Upload(ctx, store, backupID, manifestData, nil)
		require.NoError(t, err)

		assert.True(t, store.has(manifestKey))
		assert.Empty(t, store.deleted)
	})

	t.Run("rollback on manifest failure", func(t *testing.T) {
		store := newMockStorage()
		store.putErr = func(key string) error {
			if key == manifestKey {
				return fmt.Errorf("manifest storage error")
			}
			return nil
		}

		err := Upload(ctx, store, backupID, manifestData, twoArchives)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upload manifest")

		// Both archives were rolled back.
		assert.Contains(t, store.deleted, keyA)
		assert.Contains(t, store.deleted, keyB)
		assert.False(t, store.has(keyA))
		assert.False(t, store.has(keyB))
		assert.False(t, store.has(manifestKey))
	})

	t.Run("rollback on second archive failure", func(t *testing.T) {
		store := newMockStorage()
		store.putErr = func(key string) error {
			if key == keyB {
				return fmt.Errorf("storage unavailable")
			}
			return nil
		}

		err := Upload(ctx, store, backupID, manifestData, twoArchives)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upload archive for replicaset")
		assert.Contains(t, err.Error(), testRSB)

		// First archive was uploaded, then rolled back.
		assert.Contains(t, store.deleted, keyA)
		assert.False(t, store.has(keyA))
		assert.False(t, store.has(keyB))
		assert.False(t, store.has(manifestKey))
	})

	t.Run("first archive fails, nothing to rollback", func(t *testing.T) {
		store := newMockStorage()
		store.putErr = func(key string) error {
			if key == keyA {
				return fmt.Errorf("first archive fails")
			}
			return nil
		}

		err := Upload(ctx, store, backupID, manifestData, []ArchiveToUpload{
			{LocalPath: archiveA, StorageKey: keyA, Size: 9, ReplicasetUUID: testRSA},
		})
		require.Error(t, err)

		assert.Empty(t, store.deleted)
		assert.False(t, store.has(keyA))
	})
}

func TestPrepareArchives(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		backupID := BackupID("20260326T120000Z")

		path1 := writeArchiveFile(t, dir, string(backupID), testRSA, []byte("content-a"))
		path2 := writeArchiveFile(t, dir, string(backupID), testRSB, []byte("content-bb"))

		archives, locations, err := PrepareArchives([]string{path1, path2}, backupID)
		require.NoError(t, err)

		require.Len(t, archives, 2)
		require.Len(t, locations, 2)

		wantKeyA := "data/20260326T120000Z-" + testRSA + ".tar.zst"
		wantKeyB := "data/20260326T120000Z-" + testRSB + ".tar.zst"

		assert.Equal(t, path1, archives[0].LocalPath)
		assert.Equal(t, testRSA, archives[0].ReplicasetUUID)
		assert.Equal(t, int64(9), archives[0].Size)
		assert.Equal(t, wantKeyA, archives[0].StorageKey)

		assert.Equal(t, path2, archives[1].LocalPath)
		assert.Equal(t, testRSB, archives[1].ReplicasetUUID)
		assert.Equal(t, int64(10), archives[1].Size)
		assert.Equal(t, wantKeyB, archives[1].StorageKey)

		// The manifest records the same key the object is stored under, and
		// both are relative to the storage root: the
		// <cluster_name>/<environment>/ segment belongs to the storage the
		// keys are resolved against, not to the keys.
		locA := locations[testRSA]
		require.NotNil(t, locA)
		assert.Equal(t, wantKeyA, locA.Path)
		assert.Equal(t, int64(9), locA.SizeBytes)

		locB := locations[testRSB]
		require.NotNil(t, locB)
		assert.Equal(t, wantKeyB, locB.Path)
		assert.Equal(t, int64(10), locB.SizeBytes)
	})

	t.Run("empty paths", func(t *testing.T) {
		archives, locations, err := PrepareArchives(nil, "bid")
		require.NoError(t, err)
		assert.Empty(t, archives)
		assert.Empty(t, locations)
	})

	t.Run("duplicate replicaset", func(t *testing.T) {
		dir1 := t.TempDir()
		dir2 := t.TempDir()
		backupID := BackupID("bid")
		path1 := writeArchiveFile(t, dir1, string(backupID), testRSA, []byte("a"))
		path2 := writeArchiveFile(t, dir2, string(backupID), testRSA, []byte("b"))

		_, _, err := PrepareArchives([]string{path1, path2}, backupID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate archive for replicaset")
	})

	// Error cases that need a file on disk.
	errorCases := []struct {
		name     string
		filename string
		backupID string
		wantErr  string
	}{
		{"wrong extension", "bid-uuid.zip", "bid", ".tar.zst extension"},
		{"wrong backup-id prefix", "other-id-uuid.tar.zst", "bid", "does not start with backup-id"},
		{"empty uuid", "bid-.tar.zst", "bid", "empty replicaset UUID"},
	}

	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.filename)
			require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

			_, _, err := PrepareArchives([]string{path}, BackupID(tc.backupID))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	t.Run("non-existent file", func(t *testing.T) {
		_, _, err := PrepareArchives([]string{"/nonexistent/path.tar.zst"}, "bid")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stat archive")
	})
}

func TestBuildTopologyFromFragments(t *testing.T) {
	cases := []struct {
		name      string
		fragments []*Fragment
		want      map[string][]TopologyInstance
	}{
		{
			"multiple replicasets",
			[]*Fragment{
				{
					ReplicasetUUID: testRSA,
					InstanceUUID:   "inst-a",
					InstanceName:   "a-001",
					Hostname:       "host-a",
				},
				{
					ReplicasetUUID: testRSB,
					InstanceUUID:   "inst-b",
					InstanceName:   "b-001",
					Hostname:       "host-b",
				},
			},
			map[string][]TopologyInstance{
				testRSA: {{InstanceUUID: "inst-a", InstanceName: "a-001", Hostname: "host-a"}},
				testRSB: {{InstanceUUID: "inst-b", InstanceName: "b-001", Hostname: "host-b"}},
			},
		},
		{
			"empty",
			nil,
			map[string][]TopologyInstance{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topology := BuildTopologyFromFragments(tc.fragments)
			assert.Equal(t, tc.want, topology.Replicasets)
		})
	}
}

func TestReadPlan(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
		want    *BackupPlan
	}{
		{
			"valid plan",
			`{"format_version":1,"mode":"full",` +
				`"replicasets":{"rs-a":{"master_instance_uuid":"inst-a"}}}`,
			"",
			&BackupPlan{
				FormatVersion: PlanFormatVersion,
				Type:          BackupTypeFull,
				Replicasets:   map[string]ReplicasetPlan{"rs-a": {MasterInstanceUUID: "inst-a"}},
			},
		},
		{
			"invalid JSON",
			`{invalid`,
			"decode plan",
			nil,
		},
		{
			"a format version this tt does not know",
			`{"format_version":99,"mode":"full",` +
				`"replicasets":{"rs-a":{"master_instance_uuid":"inst-a"}}}`,
			"has format_version 99, this tt understands 1",
			nil,
		},
		{
			"no format version at all",
			`{"mode":"full","replicasets":{"rs-a":{"master_instance_uuid":"inst-a"}}}`,
			"has no format_version",
			nil,
		},
		{
			// JSON has no integer type, so an orchestrator building the plan
			// through jq or JavaScript writes what it means as 1 like this.
			"a whole version written as a JSON float",
			`{"format_version":1.0,"mode":"full",` +
				`"replicasets":{"rs-a":{"master_instance_uuid":"inst-a"}}}`,
			"",
			&BackupPlan{
				FormatVersion: PlanFormatVersion,
				Type:          BackupTypeFull,
				Replicasets:   map[string]ReplicasetPlan{"rs-a": {MasterInstanceUUID: "inst-a"}},
			},
		},
		{
			"a version that is not a number",
			`{"format_version":"1","mode":"full",` +
				`"replicasets":{"rs-a":{"master_instance_uuid":"inst-a"}}}`,
			`format_version must be a number, got the string "1"`,
			nil,
		},
		{
			"a fractional version",
			`{"format_version":1.5,"mode":"full",` +
				`"replicasets":{"rs-a":{"master_instance_uuid":"inst-a"}}}`,
			"format_version must be a whole number, got 1.5",
			nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o644))

			plan, err := ReadPlan(path)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want.FormatVersion, plan.FormatVersion)
			assert.Equal(t, tc.want.Type, plan.Type)
			assert.Equal(t, tc.want.Replicasets, plan.Replicasets)
		})
	}

	t.Run("non-existent file", func(t *testing.T) {
		_, err := ReadPlan("/nonexistent/plan.json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read plan")
	})
}

// headManifest is a stored backup of one replicaset, taken on instanceUUID.
func headManifest(backupID BackupID, replicasetUUID, instanceUUID string) *ClusterManifest {
	return &ClusterManifest{
		BackupID:         backupID,
		BaseFullBackupID: backupID,
		Shards: map[string]Shard{
			replicasetUUID: {Instance: &ShardInstance{InstanceUUID: instanceUUID}},
		},
	}
}

// planFor is a plan of one replicaset backed up on instanceUUID.
func planFor(
	backupType BackupType,
	previous BackupID,
	replicasetUUID, instanceUUID string,
) *BackupPlan {
	return &BackupPlan{
		FormatVersion:    PlanFormatVersion,
		Type:             backupType,
		PreviousBackupID: OptionalBackupID(previous),
		Replicasets: map[string]ReplicasetPlan{
			replicasetUUID: {MasterInstanceUUID: instanceUUID},
		},
	}
}

// Between planning and uploading another backup can land, and this host's
// clock can be behind. Both leave a chain that reads wrong afterwards, so both
// are refused before the first object is written.
func TestCheckChainHead(t *testing.T) {
	head := headManifest("20260326T120000Z", testRSA, testInstanceA)

	cases := []struct {
		name     string
		head     *ClusterManifest
		plan     *BackupPlan
		backupID BackupID
		wantErr  string
	}{
		{
			name:     "the first backup of an empty storage",
			plan:     planFor(BackupTypeFull, "", testRSA, testInstanceA),
			backupID: "20260326T120000Z",
		},
		{
			name:     "an increment continuing the head",
			head:     head,
			plan:     planFor(BackupTypeIncremental, head.BackupID, testRSA, testInstanceA),
			backupID: "20260327T120000Z",
		},
		{
			name:     "a full backup on top of a chain",
			head:     head,
			plan:     planFor(BackupTypeFull, "", testRSA, testInstanceA),
			backupID: "20260327T120000Z",
		},
		{
			name:     "the plan names a backup the storage does not hold",
			plan:     planFor(BackupTypeIncremental, "20260101T000000Z", testRSA, testInstanceA),
			backupID: "20260327T120000Z",
			wantErr:  "the storage holds no backup at all",
		},
		{
			name:     "another upload landed since the plan was made",
			head:     head,
			plan:     planFor(BackupTypeIncremental, "20260101T000000Z", testRSA, testInstanceA),
			backupID: "20260327T120000Z",
			wantErr:  "would not continue what it was planned against",
		},
		{
			name:     "a backup id that sorts below the head",
			head:     head,
			plan:     planFor(BackupTypeIncremental, head.BackupID, testRSA, testInstanceA),
			backupID: "20260101T000000Z",
			wantErr:  "the clock of this host is behind",
		},
		{
			// Ids are compared as text, which is how every reader of the
			// storage orders them. A bare counter does not sort that way, and
			// the run that would break the order is the one refused.
			name:     "an id scheme that does not sort",
			head:     headManifest("backup-2", testRSA, testInstanceA),
			plan:     planFor(BackupTypeFull, "", testRSA, testInstanceA),
			backupID: "backup-10",
			wantErr:  "not generated in an order that sorts",
		},
		{
			name:     "a backup id equal to the head",
			head:     head,
			plan:     planFor(BackupTypeFull, "", testRSA, testInstanceA),
			backupID: head.BackupID,
			wantErr:  "does not sort above",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckChainHead(tc.head, tc.plan, tc.backupID)
			if tc.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// An operator reading the storage months later cannot otherwise tell a
// scheduled full backup from one the cluster forced.
func TestPromotionWarning(t *testing.T) {
	head := headManifest("20260326T120000Z", testRSA, testInstanceA)

	t.Run("no chain to promote over", func(t *testing.T) {
		assert.Nil(t, PromotionWarning(nil, planFor(BackupTypeFull, "", testRSA, testInstanceA)))
	})

	t.Run("an increment is not a promotion", func(t *testing.T) {
		plan := planFor(BackupTypeIncremental, head.BackupID, testRSA, testInstanceA)
		assert.Nil(t, PromotionWarning(head, plan))
	})

	t.Run("a scheduled full backup of an unchanged cluster", func(t *testing.T) {
		assert.Nil(t, PromotionWarning(head, planFor(BackupTypeFull, "", testRSA, testInstanceA)))
	})

	t.Run("the replicaset set changed", func(t *testing.T) {
		plan := planFor(BackupTypeFull, "", testRSB, testInstanceA)

		warning := PromotionWarning(head, plan)
		require.NotNil(t, warning)
		assert.Equal(t, WarnPromotedToFull, warning.Code)
		assert.Equal(t, PromotedTopologyChanged, warning.Details["reason"])
	})

	t.Run("the master changed", func(t *testing.T) {
		plan := planFor(BackupTypeFull, "", testRSA, testInstanceB)

		warning := PromotionWarning(head, plan)
		require.NotNil(t, warning)
		assert.Equal(t, PromotedMasterChanged, warning.Details["reason"])
		assert.Contains(t, warning.Message, testInstanceB)
	})

	t.Run("a shard the previous backup failed on", func(t *testing.T) {
		failed := headManifest("20260326T120000Z", testRSA, testInstanceA)
		failed.Shards[testRSA] = Shard{Error: "unreachable"}

		// The backup that failed says nothing about where the master was, so
		// there is nothing to call a change.
		assert.Nil(t, PromotionWarning(failed, planFor(BackupTypeFull, "", testRSA, testInstanceB)))
	})
}

// fragmentOfPlan is a fragment of one replicaset, taken on instanceUUID.
func fragmentOfPlan(replicasetUUID, instanceUUID string, backupType BackupType) *Fragment {
	return &Fragment{
		ReplicasetUUID: replicasetUUID,
		InstanceUUID:   instanceUUID,
		Type:           backupType,
	}
}

// planOf is a plan naming one master per replicaset.
func planOf(mode BackupType, masters map[string]string) *BackupPlan {
	replicasets := make(map[string]ReplicasetPlan, len(masters))
	for replicasetUUID, master := range masters {
		replicasets[replicasetUUID] = ReplicasetPlan{MasterInstanceUUID: master}
	}

	return &BackupPlan{Type: mode, Replicasets: replicasets}
}

func TestValidateFragmentsAgainstPlan(t *testing.T) {
	cases := []struct {
		name      string
		fragments []*Fragment
		plan      *BackupPlan
		wantErr   string
		// wantUnchecked is what the plan left unstated, so nothing could be
		// compared against it.
		wantUnchecked int
	}{
		{
			name: "all covered",
			fragments: []*Fragment{
				fragmentOfPlan(testRSA, testInstanceA, BackupTypeFull),
				fragmentOfPlan(testRSB, testInstanceB, BackupTypeFull),
			},
			plan: planOf(BackupTypeFull, map[string]string{
				testRSA: testInstanceA, testRSB: testInstanceB,
			}),
		},
		{
			name:      "missing fragments",
			fragments: []*Fragment{fragmentOfPlan(testRSA, testInstanceA, BackupTypeFull)},
			plan: planOf(BackupTypeFull, map[string]string{
				testRSA: testInstanceA, testRSB: "", testRSC: "",
			}),
			wantErr: "missing for",
		},
		{
			// A failover between plan and start: the backup exists, but it
			// continues another instance's journal.
			name:      "taken on an instance the plan did not name",
			fragments: []*Fragment{fragmentOfPlan(testRSA, testInstanceB, BackupTypeFull)},
			plan:      planOf(BackupTypeFull, map[string]string{testRSA: testInstanceA}),
			wantErr:   "does not continue the chain this plan was made for",
		},
		{
			name:      "a full fragment against an incremental plan",
			fragments: []*Fragment{fragmentOfPlan(testRSA, testInstanceA, BackupTypeFull)},
			plan:      planOf(BackupTypeIncremental, map[string]string{testRSA: testInstanceA}),
			wantErr:   "the plan asks for incremental",
		},
		{
			// The worse direction: no snapshot in the archive, and a manifest
			// that says the chain starts here.
			name: "an incremental fragment against a full plan",
			fragments: []*Fragment{
				fragmentOfPlan(testRSA, testInstanceA, BackupTypeIncremental),
			},
			plan:    planOf(BackupTypeFull, map[string]string{testRSA: testInstanceA}),
			wantErr: "the plan asks for full",
		},
		{
			name: "a fragment of a replicaset the plan does not expect",
			fragments: []*Fragment{
				fragmentOfPlan(testRSA, testInstanceA, BackupTypeFull),
				fragmentOfPlan(testRSB, testInstanceB, BackupTypeFull),
			},
			plan:    planOf(BackupTypeFull, map[string]string{testRSA: testInstanceA}),
			wantErr: "is not in the plan",
		},
		{
			// A hand-written plan states less than tt writes. What it does not
			// state cannot be checked, and saying so is better than refusing
			// the plan outright.
			name:          "a plan naming no master",
			fragments:     []*Fragment{fragmentOfPlan(testRSA, testInstanceB, BackupTypeFull)},
			plan:          planOf(BackupTypeFull, map[string]string{testRSA: ""}),
			wantUnchecked: 1,
		},
		{
			name:          "a plan stating no mode",
			fragments:     []*Fragment{fragmentOfPlan(testRSA, testInstanceA, BackupTypeFull)},
			plan:          planOf("", map[string]string{testRSA: testInstanceA}),
			wantUnchecked: 1,
		},
		{
			name:      "empty plan",
			fragments: nil,
			plan:      planOf(BackupTypeFull, map[string]string{}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Authentic: the plan is tt's own, so a disagreement is a refusal.
			unchecked, err := ValidateFragmentsAgainstPlan(tc.fragments, tc.plan, true)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Len(t, unchecked, tc.wantUnchecked)
			}
		})
	}
}

func TestValidateFragmentsAgainstPlanMissingSorted(t *testing.T) {
	// Missing UUIDs must appear sorted in the error message.
	fragments := []*Fragment{{ReplicasetUUID: testRSA}}
	plan := &BackupPlan{Replicasets: map[string]ReplicasetPlan{
		testRSA: {}, testRSB: {}, testRSC: {},
	}}

	_, err := ValidateFragmentsAgainstPlan(fragments, plan, true)
	require.Error(t, err)
	assert.Less(t, strings.Index(err.Error(), testRSB), strings.Index(err.Error(), testRSC))
}

// A plan tt did not write states what the operator believes. tt compares the
// fragments against it all the same and says where they disagree, but it does
// not overrule the operator -- which is also the way out of these checks.
func TestValidateFragmentsAgainstAHandWrittenPlan(t *testing.T) {
	fragments := []*Fragment{fragmentOfPlan(testRSA, testInstanceB, BackupTypeIncremental)}
	plan := planOf(BackupTypeFull, map[string]string{testRSA: testInstanceA})

	notes, err := ValidateFragmentsAgainstPlan(fragments, plan, false)
	require.NoError(t, err)
	require.Len(t, notes, 2, "both disagreements are reported, neither is enforced")

	for _, note := range notes {
		assert.Contains(t, note, "not written by tt backup plan")
	}

	// A shard the plan does not mention is reported the same way -- the plan is
	// not authoritative about the composition either.
	notes, err = ValidateFragmentsAgainstPlan(
		[]*Fragment{
			fragmentOfPlan(testRSA, testInstanceA, BackupTypeFull),
			fragmentOfPlan(testRSB, testInstanceB, BackupTypeFull),
		}, plan, false)
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0], "is not in the plan")

	// What no plan can excuse: a replicaset it expects that produced no
	// fragment at all. That is about the inputs of this run, not about what the
	// plan believes -- a shard is missing from the backup either way.
	_, err = ValidateFragmentsAgainstPlan(nil, plan, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing for")
}

// The checksum is what tells the two apart: tt signs the plan it produces, and
// anything else -- written by hand, or edited afterwards -- does not match.
func TestPlanIsAuthentic(t *testing.T) {
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {rwInst(testInstA, "a-001")},
	})

	plan, err := Plan(BackupTypeFull, nil, live, PlanScope{ClusterName: "payments"})
	require.NoError(t, err)
	require.NotEmpty(t, plan.ChecksumSHA256)

	authentic, err := PlanIsAuthentic(plan)
	require.NoError(t, err)
	assert.True(t, authentic)

	t.Run("a plan carrying no checksum", func(t *testing.T) {
		unsigned := *plan
		unsigned.ChecksumSHA256 = ""

		authentic, err := PlanIsAuthentic(&unsigned)
		require.NoError(t, err)
		assert.False(t, authentic)
	})

	t.Run("a plan edited after it was written", func(t *testing.T) {
		edited := *plan
		edited.Replicasets = map[string]ReplicasetPlan{
			testRSa: {MasterInstanceUUID: testInstB},
		}

		authentic, err := PlanIsAuthentic(&edited)
		require.NoError(t, err)
		assert.False(t, authentic, "changing what the plan says has to break the checksum")
	})

	t.Run("a plan that only travelled", func(t *testing.T) {
		// Re-encoding and decoding is what happens between the two hosts; it
		// must not look like an edit.
		data, err := json.Marshal(plan)
		require.NoError(t, err)

		var travelled BackupPlan
		require.NoError(t, json.Unmarshal(data, &travelled))

		authentic, err := PlanIsAuthentic(&travelled)
		require.NoError(t, err)
		assert.True(t, authentic)
	})
}

func TestSplitPaths(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "/tmp/a.json", []string{"/tmp/a.json"}},
		{"multiple", "/tmp/a.json,/tmp/b.json", []string{"/tmp/a.json", "/tmp/b.json"}},
		{"with whitespace", " /tmp/a.json , /tmp/b.json ", []string{"/tmp/a.json", "/tmp/b.json"}},
		{"single with spaces", "  /tmp/a.json  ", []string{"/tmp/a.json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SplitPaths(tc.input))
		})
	}
}

func TestReadFragments(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		pathA := writeFragmentFile(t, dir, "a.json", testFragment(testRSA))
		pathB := writeFragmentFile(t, dir, "b.json", testFragment(testRSB))

		fragments, err := ReadFragments([]string{pathA, pathB})
		require.NoError(t, err)
		require.Len(t, fragments, 2)
		assert.Equal(t, testRSA, fragments[0].ReplicasetUUID)
		assert.Equal(t, testRSB, fragments[1].ReplicasetUUID)
		assert.Equal(t, BackupTypeFull, fragments[0].Type)
	})

	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"invalid JSON", "{invalid", "read fragment"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o644))

			_, err := ReadFragments([]string{path})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	t.Run("empty", func(t *testing.T) {
		fragments, err := ReadFragments(nil)
		require.NoError(t, err)
		assert.Empty(t, fragments)
	})

	t.Run("non-existent file", func(t *testing.T) {
		_, err := ReadFragments([]string{"/nonexistent/fragment.json"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read fragment")
	})
}

func TestUUIDFromArchivePath(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		backupID string
		wantUUID string
		wantErr  string
	}{
		{
			"valid",
			"/tmp/bid-11111111-1111-1111-1111-111111111111.tar.zst",
			"bid",
			"11111111-1111-1111-1111-111111111111",
			"",
		},
		{
			"with nested directory",
			"/tmp/tt-backup/bid-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.tar.zst",
			"bid",
			"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			"",
		},
		{
			"wrong extension",
			"/tmp/bid-uuid.zip",
			"bid",
			"",
			".tar.zst extension",
		},
		{
			"wrong backup-id prefix",
			"/tmp/other-id-uuid.tar.zst",
			"bid",
			"",
			"does not start with backup-id",
		},
		{
			"empty uuid after prefix",
			"/tmp/bid-.tar.zst",
			"bid",
			"",
			"empty replicaset UUID",
		},
		{
			"backup-id only, no separator",
			"/tmp/bid.tar.zst",
			"bid",
			"",
			"does not start with backup-id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uuid, err := uuidFromArchivePath(tc.path, tc.backupID)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantUUID, uuid)
		})
	}
}
