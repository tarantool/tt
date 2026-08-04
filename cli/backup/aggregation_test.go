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
}

// A warning that describes the backup rather than something missing from it
// must not degrade the status: a full backup forced by a master change holds
// every byte a planned one would.
func TestAggregateStatusIgnoresInformationalWarnings(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentA)

	aggregate := func(warnings ...Warning) *ClusterManifest {
		t.Helper()

		manifest, err := Aggregate(AggregateInput{
			BackupID:         testBackupID,
			BaseFullBackupID: testBackupID,
			CreationTime:     testCreationTime(),
			Topology:         topologyFromClusterManifestFixture(t, testRSA),
			Warnings:         warnings,
			Shards: []*ShardInput{
				{
					ReplicasetUUID: testRSA,
					Fragment:       &fragment,
					Location:       &ArtifactLocation{Path: "data/rs-a.tar.zst", SizeBytes: 42},
				},
			},
		})
		require.NoError(t, err)

		return manifest
	}

	promoted := NewPromotedToFullWarning(PromotedMasterChanged, "the master changed")

	informational := aggregate(promoted)
	require.Equal(t, StatusOK, informational.Status)
	require.Len(t, informational.Warnings, 1)

	// A blocking warning still degrades, and does so next to an informational
	// one: the two are counted separately, not as "any warning at all".
	mixed := aggregate(promoted, NewStoragePartialUploadWarning([]string{"data/x.tar.zst"}))
	require.Equal(t, StatusDegraded, mixed.Status)

	// An unknown code counts as blocking: this build cannot tell whether it
	// describes the backup or something lost from it, and calling it OK is the
	// answer that hides a problem.
	unknown := aggregate(Warning{Code: WarningCode("a_code_from_a_later_rfc")})
	require.Equal(t, StatusDegraded, unknown.Status)
}

func TestAggregateUnavailableShard(t *testing.T) {
	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards:           []*ShardInput{{ReplicasetUUID: testRSA}},
	})
	require.NoError(t, err)
	require.Equal(t, StatusFailed, manifest.Status)
	require.Equal(t, "shard unreachable", manifest.Shards[testRSA].Error)
	require.Len(t, manifest.Warnings, 1)
	require.Equal(t, WarnShardUnreachable, manifest.Warnings[0].Code)
}

// A shard that produced a fragment but still reported an error is recorded
// as an error entry with a shard_partial warning; the healthy shard keeps
// the manifest degraded rather than failed. No CLI path builds such a
// ShardInput yet -- upload refuses incomplete fragment sets -- so this pins
// the contract for the orchestrator that will.
func TestAggregatePartialShard(t *testing.T) {
	fragmentA := mustDecodeFragment(t, fixtureFragmentA)
	fragmentB := mustDecodeFragment(t, fixtureFragmentB)

	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		Topology:         topologyFromClusterManifestFixture(t, testRSA, testRSB),
		Shards: []*ShardInput{
			{
				ReplicasetUUID: testRSA,
				Fragment:       &fragmentA,
				Location:       &ArtifactLocation{Path: "data/rs-a.tar.zst", SizeBytes: 42},
			},
			{
				ReplicasetUUID: testRSB,
				Fragment:       &fragmentB,
				Err:            errors.New("wal copy interrupted"),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, StatusDegraded, manifest.Status)

	require.Len(t, manifest.Warnings, 1)
	warning := manifest.Warnings[0]
	require.Equal(t, WarnShardPartial, warning.Code)
	require.Equal(t, "wal copy interrupted", warning.Message)
	require.Equal(t, testRSB, warning.Details["replicaset_uuid"])
	require.Equal(t, fragmentB.InstanceUUID, warning.Details["instance_uuid"])

	require.NotNil(t, manifest.Shards[testRSA].Instance)
	require.Nil(t, manifest.Shards[testRSB].Instance)
	require.Equal(t, "wal copy interrupted", manifest.Shards[testRSB].Error)
}

func TestAggregateNilRecoveryPointsAddsWarningAndEmptySlice(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentWithoutRecoveryPoints)

	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
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
		Topology:         topologyFromClusterManifestFixture(t, testRSB),
		Shards:           []*ShardInput{{ReplicasetUUID: testRSB, Fragment: &fragment}},
	})
	require.ErrorContains(t, err, "does not match shard input")
}

// TestAggregateRejectsDuplicateReplicaset checks two inputs for one replicaset
// are refused. Overwriting the shard entry drops a whole shard's backup from
// the manifest with status OK and no warning, and leaves its archive an
// orphan nothing ever collects.
func TestAggregateRejectsDuplicateReplicaset(t *testing.T) {
	fragment := mustDecodeFragment(t, fixtureFragmentA)
	replica := fragment
	replica.InstanceUUID = "aaaaaaaa-0000-0000-0000-000000000002"

	_, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards: []*ShardInput{
			{
				ReplicasetUUID: testRSA,
				Fragment:       &fragment,
				Location:       &ArtifactLocation{Path: "data/rs-a.tar.zst", SizeBytes: 42},
			},
			{
				ReplicasetUUID: testRSA,
				Fragment:       &replica,
				Location:       &ArtifactLocation{Path: "data/rs-a-2.tar.zst", SizeBytes: 43},
			},
		},
	})
	require.ErrorContains(t, err, "duplicate replicaset_uuid")
	require.ErrorContains(t, err, testRSA)
}

// TestAggregateRejectsDuplicateFailedReplicaset checks the duplicate guard
// also covers shards that produced no fragment.
func TestAggregateRejectsDuplicateFailedReplicaset(t *testing.T) {
	_, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
		Topology:         topologyFromClusterManifestFixture(t, testRSA),
		Shards: []*ShardInput{
			{ReplicasetUUID: testRSA, Err: errors.New("timeout")},
			{ReplicasetUUID: testRSA},
		},
	})
	require.ErrorContains(t, err, "duplicate replicaset_uuid")
}

func TestAggregateBuildsClusterManifest(t *testing.T) {
	fragmentA := mustDecodeFragment(t, fixtureFragmentA)
	fragmentB := mustDecodeFragment(t, fixtureFragmentB)

	manifest, err := Aggregate(AggregateInput{
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		CreationTime:     testCreationTime(),
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
		manifest.Shards[testRSA].Instance.Artifact.RecoveryPoints[0].Label,
		manifest.Shards[testRSB].Instance.Artifact.RecoveryPoints[0].Label,
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
