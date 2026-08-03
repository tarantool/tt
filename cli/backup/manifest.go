package backup

import (
	"encoding/json"
	"fmt"
	"time"
)

// BackupID is an opaque identifier of a backup in a chain.
type BackupID string

// OptionalBackupID is a backup id that a document may legitimately not have:
// the first full backup of a chain has no previous backup, and a plan for one
// has no base full backup either. The manifest schema types those as
// "string | null", so an empty one is written and read as null rather than as
// "": a consumer branching on `=== null` would otherwise decide that a backup
// it holds has a predecessor named "".
//
// It stays a string type so that the internal "" means "chain start" reading,
// which every walker of a chain already does, keeps working unchanged.
type OptionalBackupID BackupID

// MarshalJSON writes an absent id as null.
func (id OptionalBackupID) MarshalJSON() ([]byte, error) {
	if id == "" {
		return []byte("null"), nil
	}

	// A Go string always encodes: invalid UTF-8 is replaced, not refused.
	data, _ := json.Marshal(string(id))

	return data, nil
}

// UnmarshalJSON reads null as an absent id, and so accepts the "" that
// manifests written before this field was nullable carry. A key that is
// missing entirely never reaches here at all -- the zero value is already
// what it means.
func (id *OptionalBackupID) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*id = ""

		return nil
	}

	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("unmarshal backup id: %w", err)
	}

	*id = OptionalBackupID(value)

	return nil
}

// ClusterManifest is the complete cluster-level backup manifest.
type ClusterManifest struct {
	SchemaVersion    int              `json:"schema_version"`
	BackupID         BackupID         `json:"backup_id"`
	PreviousBackupID OptionalBackupID `json:"previous_backup_id"`
	BaseFullBackupID BackupID         `json:"base_full_backup_id"`
	Status           Status           `json:"status"`
	CreationTime     time.Time        `json:"creation_time"`
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
