package backup

import (
	"encoding/json"
	"fmt"
	"time"
)

const artifactCompression = "zstd"

// RecoveryPoint describes one engine recovery point returned by Tarantool.
type RecoveryPoint struct {
	UUID      string `json:"uuid"`
	ReplicaID uint32 `json:"replica_id"`
	LSN       uint64 `json:"lsn"`
	Timestamp int64  `json:"timestamp"` // unix-time.
}

// Fragment is a per-replicaset backup description stored in an instance archive.
type Fragment struct {
	ReplicasetUUID string           `json:"replicaset_uuid"`
	InstanceUUID   string           `json:"instance_uuid"`
	InstanceName   string           `json:"instance_name"`
	Hostname       string           `json:"hostname"`
	Type           BackupType       `json:"type"`
	VclockBegin    Vclock           `json:"vclock_begin"`
	VclockEnd      Vclock           `json:"vclock_end"`
	Files          []string         `json:"files"`
	ChecksumSHA256 string           `json:"checksum_sha256"`
	RecoveryPoints []*RecoveryPoint `json:"recovery_points,omitempty"`
}

// AggregateInput contains all external data needed to build a manifest.
type AggregateInput struct {
	BackupID         BackupID
	BaseFullBackupID BackupID
	PreviousBackupID BackupID
	CreationTime     time.Time
	CreationDuration time.Duration
	Topology         Topology
	Shards           []*ShardInput
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

	if err := json.Unmarshal(data, &fragment); err != nil {
		return nil, fmt.Errorf("decode fragment: %w", err)
	}

	if err := fragment.Validate(); err != nil {
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
	creationDuration time.Duration,
	topology Topology,
	shards []*ShardInput,
) AggregateInput {
	return AggregateInput{
		BackupID:         backupID,
		PreviousBackupID: previousBackupID,
		BaseFullBackupID: baseFullBackupID,
		CreationTime:     creationTime,
		CreationDuration: creationDuration,
		Topology:         topology,
		Shards:           shards,
	}
}

// Aggregate builds and validates a cluster manifest from shard fragments.
func Aggregate(in AggregateInput) (*ClusterManifest, error) {
	manifest := newClusterManifest(in)

	for _, shardInput := range in.Shards {
		if err := aggregateShard(manifest, shardInput); err != nil {
			return nil, fmt.Errorf("aggregate shard: %w", err)
		}
	}

	manifest.Status = calculateStatus(manifest)
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster manifest: %w", err)
	}

	return manifest, nil
}

// newClusterManifest initializes a manifest with immutable aggregate metadata.
func newClusterManifest(in AggregateInput) *ClusterManifest {
	return &ClusterManifest{
		SchemaVersion:    SchemaVersion,
		BackupID:         in.BackupID,
		PreviousBackupID: in.PreviousBackupID,
		BaseFullBackupID: in.BaseFullBackupID,
		Status:           StatusFailed,
		CreationTime:     in.CreationTime,
		CreationDuration: in.CreationDuration,
		Shards:           make(map[string]Shard, len(in.Shards)),
		Topology:         in.Topology,
		Warnings:         make([]Warning, 0),
	}
}

// aggregateShard adds one shard input to the manifest.
func aggregateShard(manifest *ClusterManifest, shardInput *ShardInput) error {
	replicasetUUID := shardInput.ReplicasetUUID

	if shardInput.Fragment == nil {
		aggregateFailedShard(manifest, replicasetUUID, shardInput.Err)
		return nil
	}

	if shardInput.Err != nil {
		manifest.Shards[replicasetUUID] = Shard{Error: shardInput.Err.Error()}
		manifest.Warnings = append(
			manifest.Warnings,
			NewShardPartialWarning(
				replicasetUUID,
				shardInput.Fragment.InstanceUUID,
				shardInput.Err.Error(),
			),
		)
		return nil
	}

	if shardInput.Fragment.ReplicasetUUID != replicasetUUID {
		return fmt.Errorf(
			"fragment replicaset_uuid %q does not match shard input replicaset_uuid %q",
			shardInput.Fragment.ReplicasetUUID,
			replicasetUUID,
		)
	}

	aggregateSuccessfulShard(manifest, replicasetUUID, shardInput)
	return nil
}

// aggregateFailedShard adds an error result for a shard.
func aggregateFailedShard(manifest *ClusterManifest, replicasetUUID string, err error) {
	if err == nil {
		manifest.Shards[replicasetUUID] = Shard{Error: "shard unreachable"}
		manifest.Warnings = append(manifest.Warnings,
			NewShardUnreachableWarning(replicasetUUID))
		return
	}

	manifest.Shards[replicasetUUID] = Shard{Error: err.Error()}
}

// aggregateSuccessfulShard adds an instance result for a shard.
func aggregateSuccessfulShard(
	manifest *ClusterManifest,
	replicasetUUID string,
	shardInput *ShardInput,
) {
	fragment := shardInput.Fragment
	location := ArtifactLocation{}
	if shardInput.Location != nil {
		location = *shardInput.Location
	}

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
		},
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

	for _, point := range fragment.RecoveryPoints {
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
		len(manifest.Warnings) > 0 ||
		successful < len(manifest.Topology.Replicasets)
}
