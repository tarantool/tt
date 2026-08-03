package restore

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/storage"
)

const (
	// shardA and shardB are the replicaset UUIDs of the two-shard cluster the
	// plan fixtures back up.
	shardA = "aaaaaaaa-1111-1111-1111-111111111111"
	shardB = "bbbbbbbb-2222-2222-2222-222222222222"
	// shardC replaces shardB after a resharding, so that a point taken before it
	// can be laid against a cluster that no longer looks like that.
	shardC = "cccccccc-3333-3333-3333-333333333333"

	// masterOfA and masterOfB are the instance names the archives were taken on.
	masterOfA = "storage-a-001"
	masterOfB = "storage-b-001"
	masterOfC = "storage-c-001"
	// secondOfA is the other member of replicaset A: a replica the backups never
	// saw, and the instance a master change promotes.
	secondOfA = "storage-a-002"

	// replicaOfA and replicaOfB are the axes the two masters count their LSNs
	// on. They differ so that a position taken from the wrong shard cannot pass
	// for the right one.
	replicaOfA uint32 = 1
	replicaOfB uint32 = 3
	replicaOfC uint32 = 5
)

// Backup ids of the standard chain, spelled the way tt backup names them.
const (
	fullBackupID = "20260325T000000Z"
	incOneID     = "20260325T060000Z"
	incTwoID     = "20260325T120000Z"
)

// planStorage is an in-memory backup storage that records every call, so that a
// test can assert planning reads the storage and never writes to it.
type planStorage struct {
	objects map[string][]byte
	// getErr fails a read outright, the way a bucket answers a 5xx.
	getErr map[string]error
	// truncated keys open but die part-way through, which is what a connection
	// dropped mid-archive looks like.
	truncated map[string]error
	gets      []string
	puts      []string
	deletes   []string
}

func newPlanStorage() *planStorage {
	return &planStorage{
		objects:   make(map[string][]byte),
		getErr:    make(map[string]error),
		truncated: make(map[string]error),
	}
}

func (s *planStorage) List(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	objects := make([]storage.ObjectInfo, 0)
	for key, data := range s.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		objects = append(objects, storage.ObjectInfo{Key: key, Size: int64(len(data))})
	}

	slices.SortFunc(objects, func(a, b storage.ObjectInfo) int {
		return cmp.Compare(a.Key, b.Key)
	})

	return objects, nil
}

func (s *planStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	// Recorded before the lookup: a key that is not there was still asked for.
	s.gets = append(s.gets, key)

	if err := s.getErr[key]; err != nil {
		return nil, err
	}

	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrKeyNotFound
	}

	if err := s.truncated[key]; err != nil {
		return io.NopCloser(io.MultiReader(
			bytes.NewReader(data[:len(data)/2]),
			&failingStream{err: err},
		)), nil
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *planStorage) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	s.puts = append(s.puts, key)
	s.objects[key] = data

	return nil
}

func (s *planStorage) Delete(_ context.Context, key string) error {
	s.deletes = append(s.deletes, key)
	delete(s.objects, key)

	return nil
}

// errStorageDied is what a storage that stops answering mid-transfer returns.
var errStorageDied = errors.New("storage: connection reset by peer")

// failingStream stands in for an object whose transfer breaks mid-stream.
type failingStream struct {
	err error
}

func (r *failingStream) Read([]byte) (int, error) {
	return 0, r.err
}

// planFixture is a synthetic backup storage plus the local directory a plan
// downloads into. The directory does not exist yet: creating it is the plan's
// own job, and a test that pre-created it would not notice if it stopped.
type planFixture struct {
	t     *testing.T
	store *planStorage
	dir   string
}

func newPlanFixture(t *testing.T) *planFixture {
	t.Helper()

	return &planFixture{
		t:     t,
		store: newPlanStorage(),
		dir:   filepath.Join(t.TempDir(), "restore"),
	}
}

// shardBackup describes one replicaset's share of one cluster backup.
type shardBackup struct {
	// replicaset is the replicaset UUID the archive belongs to.
	replicaset string
	// master is the instance name the archive was taken on.
	master string
	// members are the instance names the manifest topology lists for the
	// replicaset. Empty means the master alone, which is what a real manifest
	// carries: topology is built from the backup fragments, one per master.
	members []string
	// generation stamps the instance UUIDs. Two fixtures with the same names
	// and different generations are the redeploy case.
	generation string
	// replicaID is the axis this shard's positions are counted on.
	replicaID uint32
	// vclockBegin and vclockEnd bound the journal the archive carries; an
	// incremental has to begin exactly where its parent ended.
	vclockBegin uint64
	vclockEnd   uint64
	// points are the recovery points inside the archive.
	points []backup.RecoveryPoint
	// absent turns the shard into a hole: the replicaset stays in the topology,
	// this backup just carries no archive for it.
	absent bool
}

// clusterBackup describes one cluster backup: one manifest and one archive per
// shard that was reachable.
type clusterBackup struct {
	id         string
	previous   string
	base       string
	backupType backup.BackupType
	createdAt  int64
	shards     []shardBackup
}

// addBackup stores one cluster backup: its archives under data/ with real
// checksums, and its manifest under manifests/.
func (f *planFixture) addBackup(spec clusterBackup) {
	f.t.Helper()

	manifest := &backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         backup.BackupID(spec.id),
		PreviousBackupID: backup.BackupID(spec.previous),
		BaseFullBackupID: backup.BackupID(spec.base),
		Status:           backup.StatusOK,
		CreationTime:     time.Unix(spec.createdAt, 0).UTC(),
		Shards:           make(map[string]backup.Shard, len(spec.shards)),
		Topology: backup.Topology{
			Replicasets: make(map[string][]backup.TopologyInstance, len(spec.shards)),
		},
		Warnings: []backup.Warning{},
	}

	for _, shard := range spec.shards {
		manifest.Topology.Replicasets[shard.replicaset] = topologyOf(shard)

		if shard.absent {
			manifest.Shards[shard.replicaset] = backup.Shard{Error: "instance unreachable"}
			manifest.Status = backup.StatusDegraded
			manifest.Warnings = append(manifest.Warnings,
				backup.NewShardUnreachableWarning(shard.replicaset))

			continue
		}

		key := storage.ArchiveKey(spec.id, shard.replicaset)
		content := archiveBody(spec.id, shard.replicaset)
		f.store.objects[key] = content

		manifest.Shards[shard.replicaset] = backup.Shard{Instance: &backup.ShardInstance{
			InstanceUUID: instanceUUIDOf(shard.generation, shard.master),
			InstanceName: shard.master,
			Hostname:     shard.master + ".example",
			VclockBegin:  vclockBeginOf(spec.backupType, shard.replicaID, shard.vclockBegin),
			VclockEnd:    backup.Vclock{shard.replicaID: shard.vclockEnd},
			Artifact: backup.Artifact{
				Path:           key,
				SizeBytes:      int64(len(content)),
				ChecksumSHA256: sha256Of(content),
				Compression:    "zstd",
				Files:          []string{"00000000000000000000.xlog"},
				RecoveryPoints: append([]backup.RecoveryPoint{}, shard.points...),
				Type:           spec.backupType,
			},
		}}
	}

	f.putManifest(manifest)
}

// putManifest (re)writes a manifest, picking up any test mutation of it.
func (f *planFixture) putManifest(manifest *backup.ClusterManifest) {
	f.t.Helper()

	data, err := json.Marshal(manifest)
	require.NoError(f.t, err)

	f.store.objects[storage.ManifestKey(string(manifest.BackupID))] = data
}

// topologyOf renders a shard's topology entry. A real manifest lists exactly
// one instance per replicaset - the master the fragment came from - so that is
// the default here; members spells out more only where a test needs them.
func topologyOf(shard shardBackup) []backup.TopologyInstance {
	names := shard.members
	if len(names) == 0 {
		names = []string{shard.master}
	}

	instances := make([]backup.TopologyInstance, 0, len(names))
	for _, name := range names {
		instances = append(instances, backup.TopologyInstance{
			InstanceUUID: instanceUUIDOf(shard.generation, name),
			InstanceName: name,
			Hostname:     name + ".example",
		})
	}

	return instances
}

// vclockBeginOf returns the vclock a backup of this type starts from. Only an
// incremental has one: a full backup starts from nothing.
func vclockBeginOf(backupType backup.BackupType, replicaID uint32, begin uint64) backup.Vclock {
	if backupType != backup.BackupTypeIncremental {
		return nil
	}

	return backup.Vclock{replicaID: begin}
}

// instanceUUIDOf derives a stable instance UUID from a deployment generation
// and an instance name, so that a redeployed cluster can be described by the
// same names under a completely different set of UUIDs.
func instanceUUIDOf(generation, name string) string {
	digest := sha256.Sum256([]byte(generation + "/" + name))
	hexed := hex.EncodeToString(digest[:])

	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32])
}

// archiveBody is the content of one replicaset's archive of one backup. It is
// unique per (backup, replicaset), so a plan that mixes two of them up is
// caught by the bytes and not only by the file name.
func archiveBody(backupID, replicasetUUID string) []byte {
	return []byte("archive of " + backupID + " for " + replicasetUUID)
}

func sha256Of(data []byte) string {
	digest := sha256.Sum256(data)

	return hex.EncodeToString(digest[:])
}

// recoveryPointAt builds one shard's recovery point. The label is what makes a
// point a cluster point: the same label on every shard is one cluster moment.
func recoveryPointAt(label string, replicaID uint32, lsn uint64, at int64) backup.RecoveryPoint {
	return backup.RecoveryPoint{
		Label:     label,
		ReplicaID: replicaID,
		LSN:       lsn,
		Timestamp: float64(at),
	}
}

// standardChain stores the chain most timing tests resolve against:
//
//	20260325T000000Z  full         created 200  cluster points "p1" at 110
//	20260325T060000Z  incremental  created 400  cluster points "p2" at 310
//	20260325T120000Z  incremental  created 600  cluster points "p3" at 510,
//	                                            "p4" at 610
//
// Both shards carry every label, so all four are cluster points. The last one
// is what makes a target of 550 resolve to p3 rather than fall past the end of
// the coverage. The two shards count on different replica ids, sit at different
// LSNs and end at different vclocks, so a position read off the wrong shard
// cannot pass for the right one.
func (f *planFixture) standardChain() {
	f.t.Helper()

	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			shardOfA(0, 1000, recoveryPointAt("p1", replicaOfA, 500, 110)),
			shardOfB(0, 900, recoveryPointAt("p1", replicaOfB, 400, 111)),
		},
	})

	f.addBackup(clusterBackup{
		id: incOneID, previous: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 400,
		shards: []shardBackup{
			shardOfA(1000, 2000, recoveryPointAt("p2", replicaOfA, 1502, 310)),
			shardOfB(900, 1800, recoveryPointAt("p2", replicaOfB, 877, 311)),
		},
	})

	f.addBackup(clusterBackup{
		id: incTwoID, previous: incOneID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 600,
		shards: []shardBackup{
			shardOfA(2000, 3000,
				recoveryPointAt("p3", replicaOfA, 2500, 510),
				recoveryPointAt("p4", replicaOfA, 2900, 610)),
			shardOfB(1800, 2700,
				recoveryPointAt("p3", replicaOfB, 2000, 511),
				recoveryPointAt("p4", replicaOfB, 2600, 611)),
		},
	})
}

// shardOfA and shardOfB describe one backup's share of the two standard
// replicasets, both taken on their first deployment's masters.
func shardOfA(begin, end uint64, points ...backup.RecoveryPoint) shardBackup {
	return shardBackup{
		replicaset: shardA, master: masterOfA, generation: "deploy-1",
		replicaID: replicaOfA, vclockBegin: begin, vclockEnd: end, points: points,
	}
}

func shardOfB(begin, end uint64, points ...backup.RecoveryPoint) shardBackup {
	return shardBackup{
		replicaset: shardB, master: masterOfB, generation: "deploy-1",
		replicaID: replicaOfB, vclockBegin: begin, vclockEnd: end, points: points,
	}
}

// chainWithAnUnreachableShard stores the chain of the RFC's own example: one
// replicaset was unreachable when the middle backup was taken, so its archives
// skip from the full straight to the last increment.
func (f *planFixture) chainWithAnUnreachableShard() {
	f.t.Helper()

	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			shardOfA(0, 1000, recoveryPointAt("p1", replicaOfA, 500, 110)),
			shardOfB(0, 900, recoveryPointAt("p1", replicaOfB, 400, 111)),
		},
	})

	absentB := shardOfB(0, 0)
	absentB.absent = true

	f.addBackup(clusterBackup{
		id: incOneID, previous: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 400,
		shards: []shardBackup{
			shardOfA(1000, 2000, recoveryPointAt("p2", replicaOfA, 1502, 310)),
			absentB,
		},
	})

	// The shard that was skipped continues from where it last succeeded, not
	// from the backup in between: that one carries nothing of it.
	f.addBackup(clusterBackup{
		id: incTwoID, previous: incOneID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 600,
		shards: []shardBackup{
			shardOfA(2000, 3000,
				recoveryPointAt("p3", replicaOfA, 2500, 510),
				recoveryPointAt("p4", replicaOfA, 2900, 610)),
			shardOfB(900, 2700,
				recoveryPointAt("p3", replicaOfB, 2000, 511),
				recoveryPointAt("p4", replicaOfB, 2600, 611)),
		},
	})
}

// chainAcrossAResharding stores a chain whose replicaset set changed: one
// replicaset was retired and another appeared. The points on either side belong
// to different clusters and are never stitched into one.
//
//	20260325T000000Z  full         topology A + B  points "p1" at 110, "p2" at 210
//	20260325T120000Z  incremental  topology A + C  point  "p3" at 510
func (f *planFixture) chainAcrossAResharding() {
	f.t.Helper()

	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			shardOfA(0, 1000,
				recoveryPointAt("p1", replicaOfA, 500, 110),
				recoveryPointAt("p2", replicaOfA, 600, 210)),
			shardOfB(0, 900,
				recoveryPointAt("p1", replicaOfB, 400, 111),
				recoveryPointAt("p2", replicaOfB, 450, 211)),
		},
	})

	f.addBackup(clusterBackup{
		id: incTwoID, previous: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 600,
		shards: []shardBackup{
			shardOfA(1000, 2000, recoveryPointAt("p3", replicaOfA, 1502, 510)),
			{
				replicaset: shardC, master: masterOfC, generation: "deploy-1",
				replicaID: replicaOfC, vclockBegin: 0, vclockEnd: 1800,
				points: []backup.RecoveryPoint{
					recoveryPointAt("p3", replicaOfC, 877, 511),
				},
			},
		},
	})
}

// chainAcrossAMasterChange stores two backups whose replicaset A was backed up
// on different instances. The points on either side are never stitched: a
// cluster whose master changed mid-window has no single state to return to.
func (f *planFixture) chainAcrossAMasterChange() {
	f.t.Helper()

	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			shardOfA(0, 1000, recoveryPointAt("p1", replicaOfA, 500, 110)),
			shardOfB(0, 900, recoveryPointAt("p1", replicaOfB, 400, 111)),
		},
	})

	promoted := shardOfA(1000, 2000, recoveryPointAt("p3", replicaOfA, 1502, 510))
	promoted.master = secondOfA

	f.addBackup(clusterBackup{
		id: incTwoID, previous: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 600,
		shards: []shardBackup{
			promoted,
			shardOfB(900, 1800, recoveryPointAt("p3", replicaOfB, 877, 511)),
		},
	})
}

// chainWithALateFirstPoint stores a chain whose full backup carries a point on
// one shard only. It is not a cluster point, so the storage covers a window
// that begins before the first moment every shard agrees on.
func (f *planFixture) chainWithALateFirstPoint() {
	f.t.Helper()

	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			shardOfA(0, 1000, recoveryPointAt("solo", replicaOfA, 500, 210)),
			shardOfB(0, 900),
		},
	})

	f.addBackup(clusterBackup{
		id: incOneID, previous: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 400,
		shards: []shardBackup{
			shardOfA(1000, 2000, recoveryPointAt("p2", replicaOfA, 1502, 310)),
			shardOfB(900, 1800, recoveryPointAt("p2", replicaOfB, 877, 311)),
		},
	})
}

// twoUnrelatedFullBackups stores two full backups that stand on nothing
// common: no chain leads from the earlier one to the later, so the window
// between them cannot be recovered to.
func (f *planFixture) twoUnrelatedFullBackups() {
	f.t.Helper()

	const earlierID = "20260324T000000Z"

	f.addBackup(clusterBackup{
		id: earlierID, base: earlierID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			shardOfA(0, 1000, recoveryPointAt("p1", replicaOfA, 500, 110)),
			shardOfB(0, 900, recoveryPointAt("p1", replicaOfB, 400, 111)),
		},
	})

	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 600,
		shards: []shardBackup{
			shardOfA(0, 4000, recoveryPointAt("p3", replicaOfA, 3500, 510)),
			shardOfB(0, 3600, recoveryPointAt("p3", replicaOfB, 3000, 511)),
		},
	})
}

// masterOnlyCluster is the configuration that matches a manifest exactly: one
// instance per replicaset, the master its archive was taken on. A manifest's
// topology is built from the backup fragments, so a master is all it knows.
func masterOnlyCluster() *ClusterTopology {
	return configuredCluster(
		configuredReplicaset("storage-a", masterOfA),
		configuredReplicaset("storage-b", masterOfB),
	)
}

// liveCluster is a realistic configuration of the same cluster: each
// replicaset carries its master plus a replica no backup ever saw.
func liveCluster() *ClusterTopology {
	return configuredCluster(
		configuredReplicaset("storage-a", masterOfA, secondOfA),
		configuredReplicaset("storage-b", masterOfB, "storage-b-002"),
	)
}

// configuredCluster builds the side of the comparison that comes from -c:
// replicaset names and the instance names in them, and nothing else. A freshly
// deployed cluster has no UUIDs to offer, which is why none appear here.
func configuredCluster(replicasets ...ConfiguredReplicaset) *ClusterTopology {
	return &ClusterTopology{Replicasets: replicasets}
}

func configuredReplicaset(name string, instances ...string) ConfiguredReplicaset {
	return ConfiguredReplicaset{Name: name, Instances: instances}
}

// plan runs the command over the fixture and asserts it succeeded.
func (f *planFixture) plan(at int64, current *ClusterTopology) *PlanResult {
	f.t.Helper()

	result, err := f.run(at, current)
	require.NoError(f.t, err)

	return result
}

// run resolves at against the fixture's storage and asserts the storage came
// out untouched: planning a restore reads backups, it never rewrites them.
func (f *planFixture) run(at int64, current *ClusterTopology) (*PlanResult, error) {
	f.t.Helper()

	result, err := Plan(f.t.Context(), PlanOpts{
		Storage:    f.store,
		TargetTime: time.Unix(at, 0).UTC(),
		Dir:        f.dir,
		Current:    current,
	})

	require.Empty(f.t, f.store.puts, "restore plan must not write to the backup storage")
	require.Empty(f.t, f.store.deletes, "restore plan must not delete from the backup storage")

	return result, err //nolint:wrapcheck
}

// downloaded returns the names of the files the plan left in --dir. A directory
// that was never created reads as no files, which is what a plan that stopped
// before downloading anything leaves behind.
func (f *planFixture) downloaded() []string {
	f.t.Helper()

	entries, err := os.ReadDir(f.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	require.NoError(f.t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	slices.Sort(names)

	return names
}

// requireNoPartials asserts nothing in --dir is a download in progress: an
// interrupted fetch must not leave a file a later run or an operator would take
// for a complete archive.
func (f *planFixture) requireNoPartials() {
	f.t.Helper()

	for _, name := range f.downloaded() {
		require.NotContains(f.t, name, downloadSuffix,
			"an interrupted download was left in %s", f.dir)
	}
}

// requireLocalCopy asserts the downloaded file holds the very bytes the storage
// serves for that archive, and that the sum the plan reports is both the sum of
// those bytes and the one the manifest recorded.
func (f *planFixture) requireLocalCopy(item DownloadItem, replicasetUUID string) {
	f.t.Helper()

	key := storage.ArchiveKey(item.BackupID, replicasetUUID)
	require.Equal(f.t, filepath.Join(f.dir, filepath.Base(key)), item.Artifact)

	data, err := os.ReadFile(item.Artifact)
	require.NoError(f.t, err)
	require.Equal(f.t, f.store.objects[key], data,
		"the downloaded copy of %s differs from the stored archive", key)
	require.Equal(f.t, sha256Of(data), item.ChecksumSHA256)
	require.Equal(f.t, f.recordedChecksum(item.BackupID, replicasetUUID),
		item.ChecksumSHA256)
}

// recordedChecksum returns the sum a manifest holds for one replicaset's
// archive, read back out of the storage the way the command reads it.
func (f *planFixture) recordedChecksum(backupID, replicasetUUID string) string {
	f.t.Helper()

	instance := f.storedManifest(backupID).Shards[replicasetUUID].Instance
	require.NotNil(f.t, instance, "backup %s carries no archive for %s",
		backupID, replicasetUUID)

	return instance.Artifact.ChecksumSHA256
}

// storedManifest decodes a stored manifest, so that a test can alter it and
// write it back the way a manifest written by an older tt would read.
func (f *planFixture) storedManifest(backupID string) *backup.ClusterManifest {
	f.t.Helper()

	data, ok := f.store.objects[storage.ManifestKey(backupID)]
	require.True(f.t, ok, "no manifest stored for backup %s", backupID)

	var manifest backup.ClusterManifest
	require.NoError(f.t, json.Unmarshal(data, &manifest))

	return &manifest
}

// archiveName is the file an archive lands in under --dir.
func archiveName(backupID, replicasetUUID string) string {
	return filepath.Base(storage.ArchiveKey(backupID, replicasetUUID))
}

// manifestName is the file a manifest lands in under --dir.
func manifestName(backupID string) string {
	return filepath.Base(storage.ManifestKey(backupID))
}

// itemIDs returns the backup ids of a shard's chain, in replay order.
func itemIDs(items []DownloadItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.BackupID)
	}

	return ids
}

// itemTypes returns the backup types of a shard's chain, in replay order.
func itemTypes(items []DownloadItem) []backup.BackupType {
	types := make([]backup.BackupType, 0, len(items))
	for _, item := range items {
		types = append(types, item.Type)
	}

	return types
}

// asJSON renders a plan the way the command prints it and decodes it back into
// bare maps, so that a test can check the document's shape and not only the Go
// struct behind it.
func asJSON(t *testing.T, result *PlanResult) map[string]any {
	t.Helper()

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var document map[string]any
	require.NoError(t, json.Unmarshal(data, &document))

	return document
}

// keysOf returns the sorted keys of a decoded JSON object.
func keysOf(t *testing.T, value any) []string {
	t.Helper()

	object, ok := value.(map[string]any)
	require.True(t, ok, "expected a JSON object, got %T", value)

	return slices.Sorted(maps.Keys(object))
}
