package chain

import (
	"bytes"
	"cmp"
	"context"
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

type memoryStorage struct {
	objects map[string][]byte
	listErr error
	getErr  map[string]error
	// getMisses[key] > 0: the next Get misses (gc race that resolves on retry).
	getMisses map[string]int
	// phantom keys are listed but never readable (gc-deleted for good).
	phantom map[string]bool
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		objects:   make(map[string][]byte),
		getErr:    make(map[string]error),
		getMisses: make(map[string]int),
		phantom:   make(map[string]bool),
	}
}

func (s *memoryStorage) List(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	objects := make([]storage.ObjectInfo, 0)
	for key, data := range s.objects {
		if strings.HasPrefix(key, prefix) {
			objects = append(objects, storage.ObjectInfo{Key: key, Size: int64(len(data))})
		}
	}
	for key := range s.phantom {
		if strings.HasPrefix(key, prefix) {
			objects = append(objects, storage.ObjectInfo{Key: key})
		}
	}
	slices.SortFunc(objects, func(a, b storage.ObjectInfo) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return objects, nil
}

func (s *memoryStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if err := s.getErr[key]; err != nil {
		return nil, err
	}
	if s.phantom[key] {
		return nil, storage.ErrKeyNotFound
	}
	if s.getMisses[key] > 0 {
		s.getMisses[key]--
		return nil, storage.ErrKeyNotFound
	}
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
	return nil
}

func (s *memoryStorage) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *memoryStorage) addManifest(t *testing.T, manifest backup.ClusterManifest) {
	t.Helper()
	s.objects[storage.ManifestKey(string(manifest.BackupID))] = encodeManifest(t, manifest)
}

func encodeManifest(t *testing.T, manifest backup.ClusterManifest) []byte {
	t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(t, err)

	var object map[string]any
	require.NoError(t, json.Unmarshal(data, &object))
	if manifest.PreviousBackupID == "" {
		object["previous_backup_id"] = nil
	}

	data, err = json.Marshal(object)
	require.NoError(t, err)
	return data
}

func manifestFixture(
	id, previous, base string,
	backupType backup.BackupType,
	createdAt, vclockBegin, vclockEnd int64,
	points map[string][]backup.RecoveryPoint,
) backup.ClusterManifest {
	manifest := backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         backup.BackupID(id),
		PreviousBackupID: backup.BackupID(previous),
		BaseFullBackupID: backup.BackupID(base),
		Status:           backup.StatusOK,
		CreationTime:     time.Unix(createdAt, 0).UTC(),
		CreationDuration: time.Second,
		Shards:           make(map[string]backup.Shard, 2),
		Topology: backup.Topology{Replicasets: map[string][]backup.TopologyInstance{
			replicasetA: {{InstanceUUID: masterA}},
			replicasetB: {{InstanceUUID: masterB}},
		}},
		Warnings: []backup.Warning{},
	}

	addShard(&manifest, replicasetA, masterA, 1,
		backupType, vclockBegin, vclockEnd, points[replicasetA])
	addShard(&manifest, replicasetB, masterB, 2,
		backupType, vclockBegin, vclockEnd, points[replicasetB])
	return manifest
}

func addShard(
	manifest *backup.ClusterManifest,
	replicasetUUID, instanceUUID string,
	replicaID uint32,
	backupType backup.BackupType,
	vclockBegin, vclockEnd int64,
	points []backup.RecoveryPoint,
) {
	manifest.Shards[replicasetUUID] = backup.Shard{Instance: &backup.ShardInstance{
		InstanceUUID: instanceUUID,
		InstanceName: instanceUUID,
		Hostname:     "localhost",
		VclockBegin:  backup.Vclock{replicaID: uint64(vclockBegin)},
		VclockEnd:    backup.Vclock{replicaID: uint64(vclockEnd)},
		Artifact: backup.Artifact{
			Path:           storage.ArchiveKey(string(manifest.BackupID), replicasetUUID),
			SizeBytes:      100,
			ChecksumSHA256: strings.Repeat("a", 64),
			Compression:    "zstd",
			Files:          []string{"00000000000000000000.xlog"},
			RecoveryPoints: append(
				make([]backup.RecoveryPoint, 0, len(points)),
				points...,
			),
			Type: backupType,
		},
	}}
}

func recoveryPoint(name string, replicaID uint32,
	lsn uint64, timestamp int64,
) backup.RecoveryPoint {
	return backup.RecoveryPoint{
		UUID:      name,
		ReplicaID: replicaID,
		LSN:       lsn,
		Timestamp: timestamp,
	}
}

// makeShardHole turns replicasetB into a degraded hole (error instead of
// instance), keeping the replicaset in topology.
func makeShardHole(manifest *backup.ClusterManifest) {
	const reason = "replicasetB unreachable"
	manifest.Shards[replicasetB] = backup.Shard{Error: reason}
	manifest.Status = backup.StatusDegraded
	manifest.Warnings = append(manifest.Warnings,
		backup.NewShardUnreachableWarning(replicasetB))
}

func buildFixtureChain(t *testing.T, manifests ...backup.ClusterManifest) *Chain {
	t.Helper()

	pointers := make([]*backup.ClusterManifest, len(manifests))
	for i := range manifests {
		pointers[i] = &manifests[i]
	}
	chain, err := Build(pointers)
	require.NoError(t, err)
	return chain
}

func findPoint(t *testing.T, chain *Chain, name string) ClusterPoint {
	t.Helper()
	for _, point := range chain.ClusterPoints() {
		if point.Name == name {
			return point
		}
	}
	t.Fatalf("cluster point %q not found", name)
	return ClusterPoint{}
}

func problemKinds(problems []*Problem) []ProblemKind {
	kinds := make([]ProblemKind, 0, len(problems))
	for _, problem := range problems {
		kinds = append(kinds, problem.Kind)
	}
	return kinds
}

func entriesByID(entries []*Entry) map[backup.BackupID]*Entry {
	result := make(map[backup.BackupID]*Entry, len(entries))
	for _, entry := range entries {
		result[entry.Manifest.BackupID] = entry
	}
	return result
}

func entryIDs(entries []*Entry) []backup.BackupID {
	ids := make([]backup.BackupID, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.Manifest.BackupID)
	}
	return ids
}

func manifestIDs(manifests []*backup.ClusterManifest) []backup.BackupID {
	ids := make([]backup.BackupID, 0, len(manifests))
	for _, manifest := range manifests {
		ids = append(ids, manifest.BackupID)
	}
	return ids
}
