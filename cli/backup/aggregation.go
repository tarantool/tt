package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	errAggregationDuplicateReplicasetUUID = errors.New("duplicate replicaset_uuid")
	errAggregationFragmentReplicasetUUID  = errors.New("fragment replicaset_uuid")
)

const artifactCompression = "zstd"

// RecoveryPoint describes one engine recovery point returned by Tarantool.
// box.backup.info() returns {timestamp, replica_id, lsn, label}.
type RecoveryPoint struct {
	Label     string  `json:"label"`
	ReplicaID uint32  `json:"replica_id"`
	LSN       uint64  `json:"lsn"`
	Timestamp float64 `json:"timestamp"` // unix-time (seconds, float).
}

// Fragment is a per-replicaset backup description stored in an instance archive.
type Fragment struct {
	ReplicasetUUID string `json:"replicaset_uuid"`
	InstanceUUID   string `json:"instance_uuid"`
	InstanceName   string `json:"instance_name"`
	// InstanceID is the backed-up instance's own box.space._cluster id, from
	// the same membership read the members came from. Zero (omitted) when
	// that read named no row for the instance.
	InstanceID uint32 `json:"instance_id,omitempty"`
	Hostname   string `json:"hostname"`
	// Members are the other members of the replicaset as the backed-up
	// snapshot records them in _cluster, ordered by name. Absent when
	// the replicaset has no other members.
	Members        []ClusterMember   `json:"members,omitempty"`
	Type           BackupType        `json:"type"`
	VclockBegin    Vclock            `json:"vclock_begin"`
	VclockEnd      Vclock            `json:"vclock_end"`
	Files          []string          `json:"files"`
	ChecksumSHA256 string            `json:"checksum_sha256"`
	RecoveryPoints *[]*RecoveryPoint `json:"recovery_points,omitempty"`
}

// AggregateInput contains all external data needed to build a manifest.
type AggregateInput struct {
	BackupID         BackupID
	BaseFullBackupID BackupID
	PreviousBackupID BackupID
	CreationTime     time.Time
	Topology         Topology
	Shards           []*ShardInput
	// Warnings are the ones the caller derived rather than the shards did --
	// today only promoted_to_full, which needs the previous manifest to see.
	Warnings []Warning
}

// ShardInput describes one expected replicaset backup result.
type ShardInput struct {
	ReplicasetUUID string
	Fragment       *Fragment
	Location       *ArtifactLocation
	Err            error
}

// ArtifactLocation identifies an already uploaded shard archive.
type ArtifactLocation struct {
	Path      string
	SizeBytes int64
}

// DecodeFragment decodes and validates one instance_backup.json payload.
func DecodeFragment(data []byte) (*Fragment, error) {
	var fragment Fragment

	err := json.Unmarshal(data, &fragment)
	if err != nil {
		return nil, fmt.Errorf("decode fragment: %w", err)
	}

	err = fragment.Validate()
	if err != nil {
		return nil, fmt.Errorf("validate fragment: %w", err)
	}

	return &fragment, nil
}

// NewAggregateInput collects backup metadata, topology and shard inputs.
func NewAggregateInput(
	backupID BackupID,
	previousBackupID BackupID,
	baseFullBackupID BackupID,
	creationTime time.Time,
	topology Topology,
	shards []*ShardInput,
) AggregateInput {
	return AggregateInput{
		BackupID:         backupID,
		PreviousBackupID: previousBackupID,
		BaseFullBackupID: baseFullBackupID,
		CreationTime:     creationTime,
		Topology:         topology,
		Shards:           shards,
		Warnings:         nil,
	}
}

// Aggregate builds and validates a cluster manifest from shard fragments.
func Aggregate(in AggregateInput) (*ClusterManifest, error) {
	manifest := newClusterManifest(in)

	for _, shardInput := range in.Shards {
		err := aggregateShard(manifest, shardInput)
		if err != nil {
			return nil, fmt.Errorf("aggregate shard: %w", err)
		}
	}

	manifest.Status = calculateStatus(manifest)

	err := manifest.Validate()
	if err != nil {
		return nil, fmt.Errorf("validate cluster manifest: %w", err)
	}

	return manifest, nil
}

// newClusterManifest initializes a manifest with immutable aggregate metadata.
func newClusterManifest(inValue AggregateInput) *ClusterManifest {
	return &ClusterManifest{
		SchemaVersion:    SchemaVersion,
		BackupID:         inValue.BackupID,
		PreviousBackupID: OptionalBackupID(inValue.PreviousBackupID),
		BaseFullBackupID: inValue.BaseFullBackupID,
		Status:           StatusFailed,
		CreationTime:     inValue.CreationTime,
		Shards:           make(map[string]Shard, len(inValue.Shards)),
		Topology:         inValue.Topology,
		Warnings:         append(make([]Warning, 0, len(inValue.Warnings)), inValue.Warnings...),
	}
}

// aggregateShard adds one shard input to the manifest.
func aggregateShard(manifest *ClusterManifest, shardInput *ShardInput) error {
	replicasetUUID := shardInput.ReplicasetUUID

	// A second input for one replicaset would overwrite the first, and the
	// shard count checks see the collapsed map, not the input: the manifest
	// would lose a whole shard with status OK and no warning, orphaning its
	// archive for good.
	if _, ok := manifest.Shards[replicasetUUID]; ok {
		return fmt.Errorf("%w %q in shard inputs",
			errAggregationDuplicateReplicasetUUID,
			replicasetUUID)
	}

	if shardInput.Fragment == nil {
		aggregateFailedShard(manifest, replicasetUUID, shardInput.Err)

		return nil
	}

	if shardInput.Err != nil {
		manifest.Shards[replicasetUUID] = Shard{Error: shardInput.Err.Error(), Instance: nil}
		manifest.Warnings = append(
			manifest.Warnings,
			NewShardPartialWarning(
				replicasetUUID,
				shardInput.Fragment.InstanceUUID,
				shardInput.Err.Error(),
			),
		)

		return nil //nolint:nilerr // A partial shard is recorded as a manifest warning.
	}

	if shardInput.Fragment.ReplicasetUUID != replicasetUUID {
		return fmt.Errorf(
			"%w %q does not match shard input replicaset_uuid "+
				"%q", errAggregationFragmentReplicasetUUID,
			shardInput.Fragment.ReplicasetUUID,
			replicasetUUID,
		)
	}

	if shardInput.Location == nil {
		reason := "fragment present but no archive location given"

		manifest.Shards[replicasetUUID] = Shard{Error: reason, Instance: nil}
		manifest.Warnings = append(
			manifest.Warnings,
			NewShardPartialWarning(replicasetUUID, shardInput.Fragment.InstanceUUID, reason),
		)

		return nil
	}

	aggregateSuccessfulShard(manifest, replicasetUUID, shardInput)

	return nil
}

// aggregateFailedShard adds an error result for a shard.
func aggregateFailedShard(manifest *ClusterManifest, replicasetUUID string, err error) {
	if err == nil {
		manifest.Shards[replicasetUUID] = Shard{Error: "shard unreachable", Instance: nil}
		manifest.Warnings = append(manifest.Warnings,
			NewShardUnreachableWarning(replicasetUUID))

		return
	}

	manifest.Shards[replicasetUUID] = Shard{Error: err.Error(), Instance: nil}
}

// aggregateSuccessfulShard adds an instance result for a shard.
func aggregateSuccessfulShard(
	manifest *ClusterManifest,
	replicasetUUID string,
	shardInput *ShardInput,
) {
	fragment := shardInput.Fragment
	location := *shardInput.Location

	manifest.Shards[replicasetUUID] = Shard{
		Instance: &ShardInstance{
			InstanceUUID: fragment.InstanceUUID,
			InstanceName: fragment.InstanceName,
			Hostname:     fragment.Hostname,
			VclockBegin:  fragment.VclockBegin,
			VclockEnd:    fragment.VclockEnd,
			Artifact: Artifact{
				Path:           location.Path,
				SizeBytes:      location.SizeBytes,
				ChecksumSHA256: fragment.ChecksumSHA256,
				Compression:    artifactCompression,
				Files:          append([]string(nil), fragment.Files...),
				RecoveryPoints: recoveryPointsFromFragment(manifest, replicasetUUID, fragment),
				Type:           fragment.Type,
			},
		}, Error: "",
	}
}

// recoveryPointsFromFragment converts optional fragment recovery points.
func recoveryPointsFromFragment(
	manifest *ClusterManifest,
	replicasetUUID string,
	fragment *Fragment,
) []RecoveryPoint {
	recoveryPoints := make([]RecoveryPoint, 0)

	if fragment.RecoveryPoints == nil {
		manifest.Warnings = append(manifest.Warnings,
			NewRecoveryPointsUnavailableWarning(replicasetUUID, "recovery points unavailable"))

		return recoveryPoints
	}

	for _, point := range *fragment.RecoveryPoints {
		if point != nil {
			recoveryPoints = append(recoveryPoints, *point)
		}
	}

	return recoveryPoints
}

// calculateStatus derives cluster backup health from shard results and warnings.
func calculateStatus(manifest *ClusterManifest) Status {
	successful := 0
	failed := 0

	for _, shard := range manifest.Shards {
		if shard.Instance != nil {
			successful++
		}

		if shard.Error != "" {
			failed++
		}
	}

	if successful == 0 {
		return StatusFailed
	}

	if isDegraded(manifest, successful, failed) {
		return StatusDegraded
	}

	return StatusOK
}

// isDegraded reports whether a partially useful manifest has issues.
func isDegraded(manifest *ClusterManifest, successful, failed int) bool {
	return failed > 0 ||
		hasBlockingWarning(manifest.Warnings) ||
		successful < len(manifest.Topology.Replicasets)
}

// hasBlockingWarning reports whether any warning says something was lost.
// An informational one does not: promoted_to_full leaves the status alone, and
// it should -- a full backup forced by a master change holds every byte a
// planned one would.
func hasBlockingWarning(warnings []Warning) bool {
	for _, warning := range warnings {
		if !warning.Code.Informational() {
			return true
		}
	}

	return false
}
