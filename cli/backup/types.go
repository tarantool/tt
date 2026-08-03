package backup

// SchemaVersion is the current cluster manifest JSON schema version.
const SchemaVersion = 1

// PlanFormatVersion is the current backup plan JSON format version. The plan
// travels from the manager host that produced it to the one that uploads,
// possibly across a tt upgrade, so upload refuses a version it does not know
// rather than reading the fields it knows and ignoring the rest.
const PlanFormatVersion PlanFormat = 1

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
//
// Tarantool 3.8.0 returns:
//   - vclock: the end vclock (max vclock to restore to);
//   - prev_vclock: the begin vclock (only for incremental);
//   - checkpoint_vclock: the begin vclock for full (cleared from output);
//   - files: full paths to snap/xlog files;
//   - recovery_points: {timestamp, replica_id, lsn, label}.
type BackupInfo struct {
	Files          []string          `json:"files"`
	Type           BackupType        `json:"type"`
	Vclock         Vclock            `json:"vclock"`
	PrevVclock     Vclock            `json:"prev_vclock"`
	RecoveryPoints *[]*RecoveryPoint `json:"recovery_points"`
}

// InstanceInfo holds instance-identifying fields and data directories fetched
// from the instance via box.info/box.cfg.
type InstanceInfo struct {
	ReplicasetUUID string
	InstanceUUID   string
	InstanceName   string
	Hostname       string
	WalDir         string
	MemtxDir       string
	VinylDir       string
}
