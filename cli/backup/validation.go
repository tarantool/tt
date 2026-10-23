package backup

import (
	"errors"
	"fmt"
)

var (
	errValidationReplicasetUUIDIsEmpty = errors.New("replicaset_uuid is empty")
	errValidationInstanceUUIDIsEmpty   = errors.New("instance_uuid is empty")
	errValidationInstanceNameIsEmpty   = errors.New("instance_name is empty")
	errValidationHostnameIsEmpty       = errors.New("hostname is empty")
	errValidationInvalidBackupType     = errors.New("invalid backup type")
	errMissingIncrementalVclock        = errors.New(
		"vclock_begin is empty for an incremental backup",
	)
	errValidationVclockEndIsEmpty         = errors.New("vclock_end is empty")
	errValidationUnsupportedSchemaVersion = errors.New("unsupported schema_version")
	errValidationBackupIDIsEmpty          = errors.New("backup_id is empty")
	errValidationBaseFullBackupIDIsEmpty  = errors.New("base_full_backup_id is empty")
	errValidationInvalidStatus            = errors.New("invalid status")
	errValidationCreationTimeIsEmpty      = errors.New("creation_time is empty")
	errValidationShardsIsNil              = errors.New("shards is nil")
	errValidationShardsIsEmpty            = errors.New("shards is empty")
	errValidationTopologyReplicasetsIsNil = errors.New("topology.replicasets is nil")
	errValidationWarningsIsNil            = errors.New("warnings is nil")
	errEmptyShardReplicasetUUID           = errors.New("shards contains empty replicaset uuid")
	errValidationShard                    = errors.New("shard")
	errValidationWarnings                 = errors.New("warnings[")
)

// Validate checks that a fragment has required structural fields.
func (fragment Fragment) Validate() error {
	if fragment.ReplicasetUUID == "" {
		return errValidationReplicasetUUIDIsEmpty
	}

	if fragment.InstanceUUID == "" {
		return errValidationInstanceUUIDIsEmpty
	}

	if fragment.InstanceName == "" {
		return errValidationInstanceNameIsEmpty
	}

	if fragment.Hostname == "" {
		return errValidationHostnameIsEmpty
	}

	if !isValidBackupType(fragment.Type) {
		return fmt.Errorf("%w %q", errValidationInvalidBackupType, fragment.Type)
	}

	// vclock_begin (box.backup.info().prev_vclock) is only present for an
	// incremental backup.
	if fragment.Type == BackupTypeIncremental && len(fragment.VclockBegin) == 0 {
		return errMissingIncrementalVclock
	}

	if len(fragment.VclockEnd) == 0 {
		return errValidationVclockEndIsEmpty
	}

	return nil
}

// Validate checks manifest structure without chain or storage verification.
func (manifest ClusterManifest) Validate() error {
	err := manifest.validateHeader()
	if err != nil {
		return fmt.Errorf("validate manifest header: %w", err)
	}

	err = manifest.validateShards()
	if err != nil {
		return fmt.Errorf("validate manifest shards: %w", err)
	}

	err = manifest.validateWarnings()
	if err != nil {
		return fmt.Errorf("validate manifest warnings: %w", err)
	}

	return nil
}

// validateHeader checks top-level manifest fields.
func (manifest ClusterManifest) validateHeader() error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w %d", errValidationUnsupportedSchemaVersion, manifest.SchemaVersion)
	}

	if manifest.BackupID == "" {
		return errValidationBackupIDIsEmpty
	}

	if manifest.BaseFullBackupID == "" {
		return errValidationBaseFullBackupIDIsEmpty
	}

	if !isValidStatus(manifest.Status) {
		return fmt.Errorf("%w %q", errValidationInvalidStatus, manifest.Status)
	}

	// Retention goes by this field, and a missing one reads as the zero time,
	// which is either "delete it, it is ancient" or "keep it forever" depending
	// on the rule. The schema requires it; catching it here keeps every consumer
	// from having to invent an answer.
	if manifest.CreationTime.IsZero() {
		return errValidationCreationTimeIsEmpty
	}

	if manifest.Shards == nil {
		return errValidationShardsIsNil
	}

	// A backup of no shards is not a backup. Left valid, such a manifest reads as
	// healthy while referring to nothing, so verify blames its archives as
	// dangling and gc collects them once they age past --orphan-age.
	if len(manifest.Shards) == 0 {
		return errValidationShardsIsEmpty
	}

	if manifest.Topology.Replicasets == nil {
		return errValidationTopologyReplicasetsIsNil
	}

	if manifest.Warnings == nil {
		return errValidationWarningsIsNil
	}

	return nil
}

// validateShards checks shard keys and shard payloads.
func (manifest ClusterManifest) validateShards() error {
	for replicasetUUID, shard := range manifest.Shards {
		err := manifest.validateShard(replicasetUUID, shard)
		if err != nil {
			return fmt.Errorf("validate shard %q: %w", replicasetUUID, err)
		}
	}

	return nil
}

// validateShard checks one shard entry.
func (manifest ClusterManifest) validateShard(replicasetUUID string, shard Shard) error {
	if replicasetUUID == "" {
		return errEmptyShardReplicasetUUID
	}

	if _, ok := manifest.Topology.Replicasets[replicasetUUID]; !ok {
		return fmt.Errorf("%w %q is not present in topology", errValidationShard, replicasetUUID)
	}

	hasInstance := shard.Instance != nil
	hasError := shard.Error != ""

	if hasInstance == hasError {
		return fmt.Errorf(
			"%w %q must contain exactly one of instance or error", errValidationShard,
			replicasetUUID,
		)
	}

	if hasInstance {
		err := validateShardInstance(replicasetUUID, *shard.Instance)
		if err != nil {
			return fmt.Errorf("validate shard %q instance: %w", replicasetUUID, err)
		}
	}

	return nil
}

// validateWarnings checks that every warning names itself.
//
// A code this build does not know is not an error. warnings[] is diagnostic:
// nothing about restoring a backup depends on it, and the spec keeps adding
// codes. Refusing an unknown one made a manifest written to a newer spec
// invalid, which is not a local matter -- the same Validate decides whether a
// chain entry is usable, so one unfamiliar word took `verify` (exit 2), `last`
// (exit 1), and, through the problem it propagates down the chain, `plan` and
// `gc` with it.
func (manifest ClusterManifest) validateWarnings() error {
	for i, warning := range manifest.Warnings {
		if warning.Code == "" {
			return fmt.Errorf("%w%d] has no code", errValidationWarnings, i)
		}
	}

	return nil
}

// validateShardInstance checks successful shard metadata.
func validateShardInstance(replicasetUUID string, instance ShardInstance) error {
	if instance.InstanceUUID == "" {
		return fmt.Errorf("%w %q instance_uuid is empty", errValidationShard, replicasetUUID)
	}

	if instance.InstanceName == "" {
		return fmt.Errorf("%w %q instance_name is empty", errValidationShard, replicasetUUID)
	}

	if instance.Hostname == "" {
		return fmt.Errorf("%w %q hostname is empty", errValidationShard, replicasetUUID)
	}

	if instance.Artifact.Type == BackupTypeIncremental && len(instance.VclockBegin) == 0 {
		return fmt.Errorf(
			"%w %q vclock_begin is empty for an incremental backup", errValidationShard,
			replicasetUUID,
		)
	}

	if len(instance.VclockEnd) == 0 {
		return fmt.Errorf("%w %q vclock_end is empty", errValidationShard, replicasetUUID)
	}

	if !isValidBackupType(instance.Artifact.Type) {
		return fmt.Errorf(
			"%w %q has invalid backup type %q", errValidationShard,
			replicasetUUID,
			instance.Artifact.Type,
		)
	}

	if instance.Artifact.RecoveryPoints == nil {
		return fmt.Errorf("%w %q artifact.recovery_points is nil",
			errValidationShard,
			replicasetUUID)
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
