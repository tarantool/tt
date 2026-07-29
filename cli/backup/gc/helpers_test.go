package gc

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

// testNow is the reference time every fixture ages its backups against.
var testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// memoryStorage is an in-memory Storage that records the order of every
// mutation, so tests can assert both what gc deleted and in which order.
type memoryStorage struct {
	objects  map[string][]byte
	modified map[string]time.Time
	// hiddenFromList keys exist but are invisible to List, reproducing a storage
	// whose listing lags behind its reads.
	hiddenFromList map[string]bool
	listErr        error
	deleteErr      map[string]error
	// deletes and gets record the calls in order.
	deletes []string
	gets    []string
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		objects:        make(map[string][]byte),
		modified:       make(map[string]time.Time),
		hiddenFromList: make(map[string]bool),
		deleteErr:      make(map[string]error),
	}
}

func (s *memoryStorage) List(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	objects := make([]storage.ObjectInfo, 0)
	for key, data := range s.objects {
		if !strings.HasPrefix(key, prefix) || s.hiddenFromList[key] {
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
	s.gets = append(s.gets, key)

	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrKeyNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memoryStorage) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	s.objects[key] = data
	s.modified[key] = testNow

	return nil
}

func (s *memoryStorage) Delete(_ context.Context, key string) error {
	if err := s.deleteErr[key]; err != nil {
		return err
	}

	s.deletes = append(s.deletes, key)
	delete(s.objects, key)

	return nil
}

// keys returns every stored key, sorted.
func (s *memoryStorage) keys() []string {
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// fixture builds a synthetic backup storage out of whole chains.
type fixture struct {
	t     *testing.T
	store *memoryStorage
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	return &fixture{t: t, store: newMemoryStorage()}
}

// backupSpec describes one stored backup.
type backupSpec struct {
	id          string
	previous    string
	base        string
	backupType  backup.BackupType
	age         time.Duration
	vclockBegin uint64
	vclockEnd   uint64
	replicasets []string
	// noCreationTime stores a manifest without the optional creation_time field.
	noCreationTime bool
}

// addChain stores a full backup followed by increments, each aged by the
// matching entry of ages.
func (f *fixture) addChain(baseID string, ages ...time.Duration) {
	f.t.Helper()

	ids := make([]string, 0, len(ages))
	for i, age := range ages {
		spec := backupSpec{
			id:          baseID,
			base:        baseID,
			backupType:  backup.BackupTypeFull,
			age:         age,
			vclockEnd:   100,
			replicasets: []string{replicasetA},
		}

		if i > 0 {
			spec.id = fmt.Sprintf("%s-inc%d", baseID, i)
			spec.previous = ids[i-1]
			spec.backupType = backup.BackupTypeIncremental
			spec.vclockBegin = uint64(i) * 100
			spec.vclockEnd = uint64(i+1) * 100
		}

		f.addBackup(spec)
		ids = append(ids, spec.id)
	}
}

// addBackup stores one manifest and the archives it refers to.
func (f *fixture) addBackup(spec backupSpec) *backup.ClusterManifest {
	f.t.Helper()

	if len(spec.replicasets) == 0 {
		spec.replicasets = []string{replicasetA}
	}

	creationTime := testNow.Add(-spec.age)
	if spec.noCreationTime {
		creationTime = time.Time{}
	}

	manifest := &backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         backup.BackupID(spec.id),
		PreviousBackupID: backup.BackupID(spec.previous),
		BaseFullBackupID: backup.BackupID(spec.base),
		Status:           backup.StatusOK,
		CreationTime:     creationTime,
		CreationDuration: time.Second,
		Shards:           make(map[string]backup.Shard, len(spec.replicasets)),
		Topology:         backup.Topology{Replicasets: map[string][]backup.TopologyInstance{}},
		Warnings:         []backup.Warning{},
	}

	for _, replicasetUUID := range spec.replicasets {
		instanceUUID := masterA
		if replicasetUUID == replicasetB {
			instanceUUID = masterB
		}

		manifest.Topology.Replicasets[replicasetUUID] = []backup.TopologyInstance{{
			InstanceUUID: instanceUUID,
			InstanceName: instanceUUID,
			Hostname:     "localhost",
		}}
		manifest.Shards[replicasetUUID] = backup.Shard{
			Instance: f.shardInstance(spec, replicasetUUID, instanceUUID),
		}
	}

	f.putObject(storage.ManifestKey(spec.id), f.encode(manifest), testNow.Add(-spec.age))

	return manifest
}

// shardInstance writes the archive of one shard and describes it.
func (f *fixture) shardInstance(
	spec backupSpec,
	replicasetUUID, instanceUUID string,
) *backup.ShardInstance {
	f.t.Helper()

	content := []byte("archive of " + spec.id + " " + replicasetUUID)
	key := storage.ArchiveKey(spec.id, replicasetUUID)
	f.putObject(key, content, testNow.Add(-spec.age))

	var vclockBegin backup.Vclock
	if spec.backupType == backup.BackupTypeIncremental {
		vclockBegin = backup.Vclock{1: spec.vclockBegin}
	}

	digest := sha256.Sum256(content)

	return &backup.ShardInstance{
		InstanceUUID: instanceUUID,
		InstanceName: instanceUUID,
		Hostname:     "localhost",
		VclockBegin:  vclockBegin,
		VclockEnd:    backup.Vclock{1: spec.vclockEnd},
		Artifact: backup.Artifact{
			Path:           key,
			SizeBytes:      int64(len(content)),
			ChecksumSHA256: hex.EncodeToString(digest[:]),
			Compression:    "zstd",
			Files:          []string{"00000000000000000000.xlog"},
			RecoveryPoints: []backup.RecoveryPoint{},
			Type:           spec.backupType,
		},
	}
}

// addArchive stores an archive of replicasetA that no manifest refers to.
func (f *fixture) addArchive(backupID string, age time.Duration) string {
	f.t.Helper()

	key := storage.ArchiveKey(backupID, replicasetA)
	f.putObject(key, []byte("dangling "+backupID), testNow.Add(-age))

	return key
}

// putObject writes an object with an explicit modification time.
func (f *fixture) putObject(key string, data []byte, modified time.Time) {
	f.t.Helper()

	f.store.objects[key] = data
	f.store.modified[key] = modified
}

func (f *fixture) encode(manifest *backup.ClusterManifest) []byte {
	f.t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(f.t, err)

	return data
}

// plan builds a deletion plan with the reference time of the fixtures.
func (f *fixture) plan(opts Options) *Plan {
	f.t.Helper()

	opts.Now = testNow

	plan, err := BuildPlan(f.t.Context(), f.store, opts)
	require.NoError(f.t, err)

	return plan
}

// run builds and executes a plan, returning both.
func (f *fixture) run(opts Options) (*Plan, *Result) {
	f.t.Helper()

	plan := f.plan(opts)

	result, err := Execute(f.t.Context(), f.store, plan)
	require.NoError(f.t, err)

	return plan, result
}

// deletedBackupIDs returns the backup ids of a plan in deletion order.
func deletedBackupIDs(plan *Plan) []string {
	ids := make([]string, 0, len(plan.Backups))
	for _, deleted := range plan.Backups {
		ids = append(ids, deleted.BackupID)
	}

	return ids
}

// orphanKeys returns the dangling archive keys of a plan.
func orphanKeys(plan *Plan) []string {
	keys := make([]string, 0, len(plan.Orphans))
	for _, orphan := range plan.Orphans {
		keys = append(keys, orphan.Key)
	}

	return keys
}

// containsNote reports whether any note mentions the given substring.
func containsNote(plan *Plan, substring string) bool {
	return slices.ContainsFunc(plan.Notes, func(note string) bool {
		return strings.Contains(note, substring)
	})
}
