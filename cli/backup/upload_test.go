package backup

import (
	"bytes"
	"context"
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
			`{"mode":"full","replicasets":{"rs-a":{"master_instance_uuid":"inst-a"}}}`,
			"",
			&BackupPlan{
				Type:        BackupTypeFull,
				Replicasets: map[string]ReplicasetPlan{"rs-a": {MasterInstanceUUID: "inst-a"}},
			},
		},
		{
			"invalid JSON",
			`{invalid`,
			"decode plan",
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

func TestValidateFragmentsAgainstPlan(t *testing.T) {
	cases := []struct {
		name      string
		fragments []*Fragment
		plan      *BackupPlan
		wantErr   string
	}{
		{
			"all covered",
			[]*Fragment{{ReplicasetUUID: testRSA}, {ReplicasetUUID: testRSB}},
			&BackupPlan{Replicasets: map[string]ReplicasetPlan{testRSA: {}, testRSB: {}}},
			"",
		},
		{
			"missing fragments",
			[]*Fragment{{ReplicasetUUID: testRSA}},
			&BackupPlan{Replicasets: map[string]ReplicasetPlan{
				testRSA: {}, testRSB: {}, testRSC: {},
			}},
			"missing for",
		},
		{
			"extra fragments are ok",
			[]*Fragment{{ReplicasetUUID: testRSA}, {ReplicasetUUID: testRSB}},
			&BackupPlan{Replicasets: map[string]ReplicasetPlan{testRSA: {}}},
			"",
		},
		{
			"empty plan",
			[]*Fragment{{ReplicasetUUID: testRSA}},
			&BackupPlan{Replicasets: map[string]ReplicasetPlan{}},
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFragmentsAgainstPlan(tc.fragments, tc.plan)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
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

	err := ValidateFragmentsAgainstPlan(fragments, plan)
	require.Error(t, err)
	assert.Less(t, strings.Index(err.Error(), testRSB), strings.Index(err.Error(), testRSC))
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
