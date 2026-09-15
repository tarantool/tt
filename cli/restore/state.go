package restore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateSchemaVersion is the current restore_state.json schema version.
const StateSchemaVersion = 1

// stateSuffix names the marker after the directory it describes, and puts it
// beside that directory rather than inside it: the instance has no use for the
// file, and several instances on one host share a parent, so a plain
// "restore_state.json" there would have them overwrite each other.
const stateSuffix = ".restore_state.json"

// State is the marker a successful apply leaves next to the work directory.
//
// It is the seam between apply and starting the cluster. A partial restore --
// one replicaset of N skipped or failed -- brings up a cluster whose shards sit
// on different states: each is self-consistent and replication is healthy, so
// nothing looks wrong until the buckets disagree. Comparing the markers of
// every restored node before the cluster is started is the orchestrator's job;
// apply only leaves the machine-readable evidence it needs. The members that
// were wiped instead of restored leave no marker, and are not missing one.
type State struct {
	SchemaVersion int `json:"schema_version"`
	// WorkDir is the base directory the restore was called with, so a marker
	// found on its own still says what it belongs to.
	WorkDir string `json:"work_dir"`
	// SnapshotDir, WALDir and VinylDir are the directories the files were
	// actually put in. They are what makes the marker readable on a split
	// layout, where WorkDir names none of them.
	SnapshotDir string `json:"snapshot_dir"`
	WALDir      string `json:"wal_dir"`
	VinylDir    string `json:"vinyl_dir"`
	// InstanceName is the instance whose configuration the directories were
	// resolved from, empty when they were given directly.
	InstanceName string `json:"instance_name,omitempty"`
	// PointName is the cluster recovery point (recovery_point.name of the
	// restore plan). It is what makes the cross-node comparison meaningful:
	// TargetPoint differs per replicaset, the name does not. Empty when the
	// operator did not pass --point-name.
	PointName string `json:"point_name,omitempty"`
	// TargetPoint is the per-replicaset position the final xlog was cut at,
	// nil when the chain was replayed whole.
	TargetPoint *Point `json:"target_point"`
	// InstanceUUID is the UUID stamped into the headers, empty when
	// --patch-uuid was omitted.
	InstanceUUID string `json:"instance_uuid,omitempty"`
	// Archives lists the applied chain in order, by base name.
	Archives  []string  `json:"archives"`
	AppliedAt time.Time `json:"applied_at"`
}

// StatePath returns the marker path for a snapshot directory.
//
// The snapshot directory is the anchor because the marker describes one
// instance and that directory belongs to one instance by construction, while
// the base directory a restore is called with can be shared by every instance
// of an application -- a marker anchored there would be overwritten by the
// next node restored on the same host. An instance whose directories are all
// one is the case where the two anchors coincide.
//
// The directory is resolved first. A relative one names a directory just as
// well as an absolute one, and appending the suffix to it would put the marker
// somewhere else entirely: "." would name a file inside the directory the
// marker describes, ".." one inside its child.
func StatePath(snapshotDir string) string {
	resolved, err := filepath.Abs(snapshotDir)
	if err != nil {
		// Only a working directory that no longer exists gets here, and the
		// unresolved name is still the one the caller passed.
		resolved = filepath.Clean(snapshotDir)
	}

	return resolved + stateSuffix
}

// writeState writes the marker to path. It is the last thing a successful
// apply does, so the file's presence means the restore is complete.
func writeState(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal restore state: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("failed to write restore state %q: %w", path, err)
	}

	return nil
}

// removeState drops a previous run's marker. Apply does this before it starts
// rebuilding the directories, so a run that dies halfway leaves no marker
// claiming they are ready.
func removeState(snapshotDir string) error {
	path := StatePath(snapshotDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove stale restore state %q: %w", path, err)
	}

	return nil
}

// ReadState decodes the marker next to a snapshot directory.
func ReadState(snapshotDir string) (*State, error) {
	path := StatePath(snapshotDir)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read restore state %q: %w", path, err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to decode restore state %q: %w", path, err)
	}

	return &state, nil
}
