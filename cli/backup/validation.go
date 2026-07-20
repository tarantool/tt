package backup

import "fmt"

// Validate checks that a fragment has required structural fields.
func (fragment Fragment) Validate() error {
	if fragment.ReplicasetUUID == "" {
		return fmt.Errorf("replicaset_uuid is empty")
	}
	if fragment.InstanceUUID == "" {
		return fmt.Errorf("instance_uuid is empty")
	}
	if fragment.InstanceName == "" {
		return fmt.Errorf("instance_name is empty")
	}
	if fragment.Hostname == "" {
		return fmt.Errorf("hostname is empty")
	}
	if !isValidBackupType(fragment.Type) {
		return fmt.Errorf("invalid backup type %q", fragment.Type)
	}
	if len(fragment.VclockBegin) == 0 {
		return fmt.Errorf("vclock_begin is empty")
	}
	if len(fragment.VclockEnd) == 0 {
		return fmt.Errorf("vclock_end is empty")
	}

	return nil
}

// Validate checks manifest structure without chain or storage verification.
func (manifest ClusterManifest) Validate() error {
	if err := manifest.validateHeader(); err != nil {
		return fmt.Errorf("validate manifest header: %w", err)
	}
	if err := manifest.validateShards(); err != nil {
		return fmt.Errorf("validate manifest shards: %w", err)
	}
	if err := manifest.validateWarnings(); err != nil {
		return fmt.Errorf("validate manifest warnings: %w", err)
	}
	return nil
}

// validateHeader checks top-level manifest fields.
func (manifest ClusterManifest) validateHeader() error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", manifest.SchemaVersion)
	}
	if manifest.BackupID == "" {
		return fmt.Errorf("backup_id is empty")
	}
	if manifest.BaseFullBackupID == "" {
		return fmt.Errorf("base_full_backup_id is empty")
	}
	if !isValidStatus(manifest.Status) {
		return fmt.Errorf("invalid status %q", manifest.Status)
	}
	if manifest.Shards == nil {
		return fmt.Errorf("shards is nil")
	}
	if manifest.Topology.Replicasets == nil {
		return fmt.Errorf("topology.replicasets is nil")
	}
	if manifest.Warnings == nil {
		return fmt.Errorf("warnings is nil")
	}
	return nil
}

// validateShards checks shard keys and shard payloads.
func (manifest ClusterManifest) validateShards() error {
	for replicasetUUID, shard := range manifest.Shards {
		if err := manifest.validateShard(replicasetUUID, shard); err != nil {
			return fmt.Errorf("validate shard %q: %w", replicasetUUID, err)
		}
	}
	return nil
}

// validateShard checks one shard entry.
func (manifest ClusterManifest) validateShard(replicasetUUID string, shard Shard) error {
	if replicasetUUID == "" {
		return fmt.Errorf("shards contains empty replicaset uuid")
	}
	if _, ok := manifest.Topology.Replicasets[replicasetUUID]; !ok {
		return fmt.Errorf("shard %q is not present in topology", replicasetUUID)
	}

	hasInstance := shard.Instance != nil
	hasError := shard.Error != ""
	if hasInstance == hasError {
		return fmt.Errorf(
			"shard %q must contain exactly one of instance or error",
			replicasetUUID,
		)
	}
	if hasInstance {
		if err := validateShardInstance(replicasetUUID, *shard.Instance); err != nil {
			return fmt.Errorf("validate shard %q instance: %w", replicasetUUID, err)
		}
	}
	return nil
}

// validateWarnings checks warning codes.
func (manifest ClusterManifest) validateWarnings() error {
	for i, warning := range manifest.Warnings {
		if !isValidWarningCode(warning.Code) {
			return fmt.Errorf("warnings[%d] has invalid code %q", i, warning.Code)
		}
	}
	return nil
}

// validateShardInstance checks successful shard metadata.
func validateShardInstance(replicasetUUID string, instance ShardInstance) error {
	if instance.InstanceUUID == "" {
		return fmt.Errorf("shard %q instance_uuid is empty", replicasetUUID)
	}
	if instance.InstanceName == "" {
		return fmt.Errorf("shard %q instance_name is empty", replicasetUUID)
	}
	if instance.Hostname == "" {
		return fmt.Errorf("shard %q hostname is empty", replicasetUUID)
	}
	if len(instance.VclockBegin) == 0 {
		return fmt.Errorf("shard %q vclock_begin is empty", replicasetUUID)
	}
	if len(instance.VclockEnd) == 0 {
		return fmt.Errorf("shard %q vclock_end is empty", replicasetUUID)
	}
	if !isValidBackupType(instance.Artifact.Type) {
		return fmt.Errorf(
			"shard %q has invalid backup type %q",
			replicasetUUID,
			instance.Artifact.Type,
		)
	}
	if instance.Artifact.RecoveryPoints == nil {
		return fmt.Errorf("shard %q artifact.recovery_points is nil", replicasetUUID)
	}

	return nil
}

// isValidBackupType reports whether backupType is known by this schema.
func isValidBackupType(backupType BackupType) bool {
	switch backupType {
	case BackupTypeFull, BackupTypeIncremental:
		return true
	default:
		return false
	}
}

// isValidStatus reports whether status is known by this schema.
func isValidStatus(status Status) bool {
	switch status {
	case StatusOK, StatusDegraded, StatusFailed:
		return true
	default:
		return false
	}
}

// isValidWarningCode reports whether code is known by this schema.
func isValidWarningCode(code WarningCode) bool {
	switch code {
	case WarnShardPartial,
		WarnShardUnreachable,
		WarnRecoveryPointsUnavailable,
		WarnStoragePartialUpload:
		return true
	default:
		return false
	}
}
