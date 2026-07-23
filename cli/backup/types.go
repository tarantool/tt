package backup

// SchemaVersion is the current cluster manifest JSON schema version.
const SchemaVersion = 1

const (
	// BackupTypeFull marks a complete backup chain starting point.
	BackupTypeFull BackupType = "full"
	// BackupTypeIncremental marks a backup based on a previous one.
	BackupTypeIncremental BackupType = "incremental"
)

const (
	// StatusOK means all expected shards were backed up without warnings.
	StatusOK Status = "OK"
	// StatusDegraded means some data exists, but the backup has warnings or shard errors.
	StatusDegraded Status = "degraded"
	// StatusFailed means no shard was successfully backed up.
	StatusFailed Status = "failed"
)

// Vclock maps replica IDs to their LSNs, including replica 0.
type Vclock map[uint32]uint64

// BackupType is the backup mode: full or incremental.
type BackupType string

// Status is the aggregate health of the cluster backup.
type Status string

// BackupInfo is the decoded box.backup.info() result: files, vclocks, type
// and recovery points of an open backup.
type BackupInfo struct {
	Files          []string          `json:"files"`
	Type           BackupType        `json:"type"`
	VclockBegin    Vclock            `json:"vclock_begin"`
	VclockEnd      Vclock            `json:"vclock_end"`
	RecoveryPoints *[]*RecoveryPoint `json:"recovery_points"`
}

// InstanceInfo holds instance-identifying fields and WAL directories fetched
// from the instance via box.info.
type InstanceInfo struct {
	ReplicasetUUID string
	InstanceUUID   string
	InstanceName   string
	Hostname       string
	WalDir         string
	MemtxDir       string
}
