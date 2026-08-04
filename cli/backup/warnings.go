package backup

// WarningCode identifies a non-fatal manifest issue.
type WarningCode string

const (
	// WarnShardPartial marks a shard backup that completed only partially.
	WarnShardPartial WarningCode = "shard_partial"
	// WarnShardUnreachable marks a shard that did not produce a fragment.
	WarnShardUnreachable WarningCode = "shard_unreachable"
	// WarnRecoveryPointsUnavailable marks a missing recovery_points field.
	WarnRecoveryPointsUnavailable WarningCode = "recovery_points_unavailable"
	// WarnStoragePartialUpload marks archives that were not fully uploaded.
	WarnStoragePartialUpload WarningCode = "storage_partial_upload"
	// WarnPromotedToFull marks a full backup taken because an incremental one
	// had become impossible: the replicaset set or a master changed since the
	// backup this chain would have continued.
	WarnPromotedToFull WarningCode = "promoted_to_full"
)

// Reasons a full backup was not the plan.
const (
	// PromotedTopologyChanged: the replicaset set is not the one backed up.
	PromotedTopologyChanged = "topology_changed"
	// PromotedMasterChanged: a replicaset is backed up on another instance now.
	PromotedMasterChanged = "master_changed"
)

// Informational reports whether a warning describes the backup rather than
// something missing from it. Only these leave the status alone: a full backup
// forced by a master change is as complete as a planned one.
func (code WarningCode) Informational() bool {
	return code == WarnPromotedToFull
}

// Warning is a typed, serializable non-fatal backup issue.
type Warning struct {
	Code    WarningCode    `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// NewShardPartialWarning reports a partial shard backup.
func NewShardPartialWarning(replicasetUUID, instanceUUID, reason string) Warning {
	return Warning{
		Code:    WarnShardPartial,
		Message: reason,
		Details: map[string]any{
			"replicaset_uuid": replicasetUUID,
			"instance_uuid":   instanceUUID,
		},
	}
}

// NewShardUnreachableWarning reports a missing shard fragment.
func NewShardUnreachableWarning(replicasetUUID string) Warning {
	return Warning{
		Code:    WarnShardUnreachable,
		Message: "shard unreachable",
		Details: map[string]any{
			"replicaset_uuid": replicasetUUID,
		},
	}
}

// NewRecoveryPointsUnavailableWarning reports unavailable recovery points.
func NewRecoveryPointsUnavailableWarning(replicasetUUID, errMsg string) Warning {
	return Warning{
		Code:    WarnRecoveryPointsUnavailable,
		Message: errMsg,
		Details: map[string]any{
			"replicaset_uuid": replicasetUUID,
		},
	}
}

// NewStoragePartialUploadWarning reports storage keys that failed to upload.
func NewStoragePartialUploadWarning(keys []string) Warning {
	return Warning{
		Code:    WarnStoragePartialUpload,
		Message: "storage partial upload",
		Details: map[string]any{
			"keys": keys,
		},
	}
}

// NewPromotedToFullWarning records why a full backup landed on top of an
// existing chain: an operator reading the storage months later cannot
// otherwise tell a scheduled full backup from one the cluster forced.
func NewPromotedToFullWarning(reason, message string) Warning {
	return Warning{
		Code:    WarnPromotedToFull,
		Message: message,
		Details: map[string]any{
			"reason": reason,
		},
	}
}
