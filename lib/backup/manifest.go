package backup

import "time"

// BackupID is an opaque identifier of a backup in a chain.
type BackupID string

// ClusterManifest is the complete cluster-level backup manifest.
type ClusterManifest struct {
	SchemaVersion    int              `json:"schema_version"`
	BackupID         BackupID         `json:"backup_id"`
	PreviousBackupID BackupID         `json:"previous_backup_id"`
	BaseFullBackupID BackupID         `json:"base_full_backup_id"`
	Status           Status           `json:"status"`
	CreationTime     time.Time        `json:"creation_time"`
	CreationDuration time.Duration    `json:"creation_duration"`
	Shards           map[string]Shard `json:"shards"` // Key is replicaset.
	Topology         Topology         `json:"topology"`
	Warnings         []Warning        `json:"warnings"` // Empty is [].
}

// Shard is one replicaset result: either a backed-up instance or an error.
type Shard struct {
	Instance *ShardInstance `json:"instance,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// ShardInstance contains successful backup metadata for one instance.
type ShardInstance struct {
	InstanceUUID string   `json:"instance_uuid"`
	InstanceName string   `json:"instance_name"`
	Hostname     string   `json:"hostname"`
	VclockBegin  Vclock   `json:"vclock_begin"`
	VclockEnd    Vclock   `json:"vclock_end"`
	Artifact     Artifact `json:"artifact"`
}

// Artifact describes the stored archive produced for a shard.
type Artifact struct {
	Path           string          `json:"path"`
	SizeBytes      int64           `json:"size_bytes"`
	ChecksumSHA256 string          `json:"checksum_sha256"`
	Compression    string          `json:"compression"`
	Files          []string        `json:"files"`
	RecoveryPoints []RecoveryPoint `json:"recovery_points"`
	Type           BackupType      `json:"type"`
}

// Topology lists expected instances grouped by replicaset UUID.
type Topology struct {
	Replicasets map[string][]TopologyInstance `json:"replicasets"`
}

// TopologyInstance is one expected instance in cluster topology.
type TopologyInstance struct {
	InstanceUUID string `json:"instance_uuid"`
	InstanceName string `json:"instance_name"`
	Hostname     string `json:"hostname"`
}
