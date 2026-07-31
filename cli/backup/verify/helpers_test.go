package verify

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/storage"
)

const (
	replicasetA = "11111111-1111-1111-1111-111111111111"
	replicasetB = "22222222-2222-2222-2222-222222222222"
	masterA     = "aaaaaaaa-0000-0000-0000-000000000001"
	masterB     = "bbbbbbbb-0000-0000-0000-000000000001"
)

// memoryStorage is an in-memory Storage that also records every mutation, so
// that tests can assert verify is read-only.
type memoryStorage struct {
	objects  map[string][]byte
	modified map[string]time.Time
	// readErrs[key] makes Get succeed but the returned reader fail mid-stream.
	readErrs map[string]error
	listErr  error
	// puts and deletes record mutations; verify must leave both empty.
	puts    []string
	deletes []string
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		objects:  make(map[string][]byte),
		modified: make(map[string]time.Time),
		readErrs: make(map[string]error),
	}
}

func (s *memoryStorage) List(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	objects := make([]storage.ObjectInfo, 0)
	for key, data := range s.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		objects = append(objects, storage.ObjectInfo{
			Key:          key,
			Size:         int64(len(data)),
			LastModified: s.modified[key],
		})
	}

	slices.SortFunc(objects, func(a, b storage.ObjectInfo) int {
		return cmp.Compare(a.Key, b.Key)
	})

	return objects, nil
}

func (s *memoryStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrKeyNotFound
	}

	if err := s.readErrs[key]; err != nil {
		return io.NopCloser(io.MultiReader(
			bytes.NewReader(data),
			&failingReader{err: err},
		)), nil
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memoryStorage) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	s.puts = append(s.puts, key)
	s.objects[key] = data
	s.modified[key] = time.Unix(0, 0).UTC()

	return nil
}

func (s *memoryStorage) Delete(_ context.Context, key string) error {
	s.deletes = append(s.deletes, key)
	delete(s.objects, key)

	return nil
}

// snapshot copies the stored keys and their contents for a mutation check.
func (s *memoryStorage) snapshot() map[string]string {
	objects := make(map[string]string, len(s.objects))
	for key, data := range s.objects {
		objects[key] = string(data)
	}

	return objects
}

// failingReader always fails, simulating an archive truncated in transit.
type failingReader struct {
	err error
}

func (r *failingReader) Read([]byte) (int, error) {
	return 0, r.err
}

// fixture builds a synthetic backup storage: manifests under manifests/ and
// their archives under data/, with real checksums.
type fixture struct {
	t     *testing.T
	store *memoryStorage
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	return &fixture{t: t, store: newMemoryStorage()}
}

// addBackup stores a healthy one-shard backup and returns its manifest.
func (f *fixture) addBackup(
	id, previous, base string,
	backupType backup.BackupType,
	vclockBegin, vclockEnd uint64,
) *backup.ClusterManifest {
	f.t.Helper()

	manifest := &backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         backup.BackupID(id),
		PreviousBackupID: backup.BackupID(previous),
		BaseFullBackupID: backup.BackupID(base),
		Status:           backup.StatusOK,
		CreationTime:     time.Unix(0, 0).UTC(),
		Shards:           make(map[string]backup.Shard, 1),
		Topology: backup.Topology{Replicasets: map[string][]backup.TopologyInstance{
			replicasetA: {{
				InstanceUUID: masterA,
				InstanceName: masterA,
				Hostname:     "localhost",
			}},
		}},
		Warnings: []backup.Warning{},
	}

	content := []byte("archive of " + id)
	key := storage.ArchiveKey(id, replicasetA)
	manifest.Shards[replicasetA] = backup.Shard{Instance: &backup.ShardInstance{
		InstanceUUID: masterA,
		InstanceName: masterA,
		Hostname:     "localhost",
		VclockBegin:  vclockBeginOf(backupType, vclockBegin),
		VclockEnd:    backup.Vclock{1: vclockEnd},
		Artifact: backup.Artifact{
			Path:           key,
			SizeBytes:      int64(len(content)),
			ChecksumSHA256: checksumOf(content),
			Compression:    "zstd",
			Files:          []string{"00000000000000000000.xlog"},
			RecoveryPoints: []backup.RecoveryPoint{},
			Type:           backupType,
		},
	}}

	f.putObject(key, content)
	f.putManifest(manifest)

	return manifest
}

// vclockBeginOf returns the vclock a backup of this type starts from. Only an
// incremental has one: a full backup starts from nothing.
func vclockBeginOf(backupType backup.BackupType, vclockBegin uint64) backup.Vclock {
	if backupType != backup.BackupTypeIncremental {
		return nil
	}

	return backup.Vclock{1: vclockBegin}
}

// mustJSON serializes a manifest for tests that store it under a key of their
// own choosing rather than the one its backup id implies.
func mustJSON(t *testing.T, manifest *backup.ClusterManifest) []byte {
	t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(t, err)

	return data
}

// putManifest (re)writes a manifest, picking up any test mutation of it.
func (f *fixture) putManifest(manifest *backup.ClusterManifest) {
	f.t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(f.t, err)

	f.putObject(storage.ManifestKey(string(manifest.BackupID)), data)
}

// putObject writes an object directly, bypassing the mutation log.
func (f *fixture) putObject(key string, data []byte) {
	f.t.Helper()

	f.store.objects[key] = data
	f.store.modified[key] = time.Unix(0, 0).UTC()
}

// verify runs the health check and asserts the storage was left untouched.
func (f *fixture) verify() *Report {
	f.t.Helper()

	before := f.store.snapshot()

	report, err := Verify(f.t.Context(), f.store)
	require.NoError(f.t, err)

	require.Empty(f.t, f.store.deletes, "verify must not delete anything")
	require.Empty(f.t, f.store.puts, "verify must not write anything")
	require.Equal(f.t, before, f.store.snapshot(), "verify must not change the storage")

	return report
}

func checksumOf(data []byte) string {
	digest := sha256.Sum256(data)

	return hex.EncodeToString(digest[:])
}

// issueKinds returns the kinds of the reported issues in report order.
func issueKinds(report *Report) []IssueKind {
	kinds := make([]IssueKind, 0, len(report.Issues))
	for _, issue := range report.Issues {
		kinds = append(kinds, issue.Kind)
	}

	return kinds
}

// findIssue returns the only issue of the given kind.
func findIssue(t *testing.T, report *Report, kind IssueKind) Issue {
	t.Helper()

	found := make([]Issue, 0, 1)
	for _, issue := range report.Issues {
		if issue.Kind == kind {
			found = append(found, issue)
		}
	}

	require.Len(t, found, 1, "expected exactly one %q issue in %v", kind, report.Issues)

	return found[0]
}
