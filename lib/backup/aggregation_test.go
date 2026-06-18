package backup

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAggregateSuccessfulManifest(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentA)

	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: 2300 * time.Millisecond,
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards: []*ShardInput{
			{
				ReplicasetUUID: testRSA,
				Fragment:       &fragment,
				Location:       &ArtifactLocation{Path: "data/rs-a.tar.zst", SizeBytes: 42},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, SchemaVersion, manifest.SchemaVersion)
	require.Equal(t, StatusOK, manifest.Status)
	require.Empty(t, manifest.Warnings)

	shard := manifest.Shards[testRSA]
	require.NotNil(t, shard.Instance)
	require.Equal(t, "data/rs-a.tar.zst", shard.Instance.Artifact.Path)
	require.Equal(t, int64(42), shard.Instance.Artifact.SizeBytes)
	require.Equal(t, "zstd", shard.Instance.Artifact.Compression)
	require.Len(t, shard.Instance.Artifact.RecoveryPoints, 2)
	require.Equal(t, 2300*time.Millisecond, manifest.CreationDuration)
}

func TestAggregateUnavailableShard(t *testing.T) {
	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: time.Second,
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards:           []*ShardInput{{ReplicasetUUID: testRSA}},
	})
	require.NoError(t, err)
	require.Equal(t, StatusFailed, manifest.Status)
	require.Equal(t, "shard unreachable", manifest.Shards[testRSA].Error)
	require.Len(t, manifest.Warnings, 1)
	require.Equal(t, WarnShardUnreachable, manifest.Warnings[0].Code)
}

func TestAggregateNilRecoveryPointsAddsWarningAndEmptySlice(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentWithoutRecoveryPoints)

	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: time.Second,
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards:           []*ShardInput{{ReplicasetUUID: testRSA, Fragment: &fragment}},
	})
	require.NoError(t, err)
	require.Equal(t, StatusDegraded, manifest.Status)
	require.Len(t, manifest.Warnings, 1)
	require.Equal(t, WarnRecoveryPointsUnavailable, manifest.Warnings[0].Code)
	require.NotNil(t, manifest.Shards[testRSA].Instance.Artifact.RecoveryPoints)
	require.Empty(t, manifest.Shards[testRSA].Instance.Artifact.RecoveryPoints)
}

func TestAggregateEmptyRecoveryPointsDoesNotAddWarning(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentWithEmptyRecoveryPoints)

	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: time.Second,
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards:           []*ShardInput{{ReplicasetUUID: testRSA, Fragment: &fragment}},
	})
	require.NoError(t, err)
	require.Equal(t, StatusOK, manifest.Status)
	require.Empty(t, manifest.Warnings)
	require.NotNil(t, manifest.Shards[testRSA].Instance.Artifact.RecoveryPoints)
}

func TestAggregateShardErrorUsesErrorShard(t *testing.T) {
	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: time.Second,
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards: []*ShardInput{{
			ReplicasetUUID: testRSA,
			Err:            errors.New("timeout: replicaset unreachable"),
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "timeout: replicaset unreachable", manifest.Shards[testRSA].Error)
	require.Equal(t, StatusFailed, manifest.Status)
	require.Empty(t, manifest.Warnings)
}

func TestAggregateRejectsInvalidFragment(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentA)
	fragment.Type = BackupType("bad")

	_, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: time.Second,
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards:           []*ShardInput{{ReplicasetUUID: testRSA, Fragment: &fragment}},
	})
	require.ErrorContains(t, err, "invalid backup type")
}

func TestAggregateRejectsReplicasetMismatch(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentA)

	_, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: time.Second,
		Topology:         topologyFromClusterManifestFixture(t, testRSB),
		Shards:           []*ShardInput{{ReplicasetUUID: testRSB, Fragment: &fragment}},
	})
	require.ErrorContains(t, err, "does not match shard input")
}

func TestAggregateBuildsClusterManifest(t *testing.T) {
	fragmentA := mustDecodeFragment(t, fixtureFragmentA)
	fragmentB := mustDecodeFragment(t, fixtureFragmentB)

	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		CreationDuration: 2300 * time.Millisecond,
		Topology:         topologyFromClusterManifestFixture(t, testRSA, testRSB, testRSC),
		Shards: []*ShardInput{
			{
				ReplicasetUUID: testRSA,
				Fragment:       &fragmentA,
				Location: &ArtifactLocation{
					Path:      "20260312T120000Z-replicaset_A_uuid.tar.zst",
					SizeBytes: 104857600,
				},
			},
			{
				ReplicasetUUID: testRSB,
				Fragment:       &fragmentB,
				Location: &ArtifactLocation{
					Path:      "20260312T120000Z-replicaset_B_uuid.tar.zst",
					SizeBytes: 98304000,
				},
			},
			{ReplicasetUUID: testRSC, Err: errors.New("timeout: replicaset unreachable")},
		},
	})
	require.NoError(t, err)
	require.NoError(t, manifest.Validate())
	require.Equal(t, StatusDegraded, manifest.Status)
	require.Equal(t, "timeout: replicaset unreachable", manifest.Shards[testRSC].Error)
	require.Equal(
		t,
		manifest.Shards[testRSA].Instance.Artifact.RecoveryPoints[0].UUID,
		manifest.Shards[testRSB].Instance.Artifact.RecoveryPoints[0].UUID,
	)
}

func topologyFromClusterManifestFixture(t *testing.T, replicasetUUIDs ...string) Topology {
	t.Helper()

	manifest := mustDecodeClusterManifest(t, fixtureClusterManifest)
	topology := Topology{Replicasets: make(map[string][]TopologyInstance, len(replicasetUUIDs))}
	for _, replicasetUUID := range replicasetUUIDs {
		topology.Replicasets[replicasetUUID] = manifest.Topology.Replicasets[replicasetUUID]
	}
	return topology
}

func testCreationTime() time.Time {
	return time.Date(2026, 3, 12, 12, 0, 2, 456000000, time.UTC)
}
