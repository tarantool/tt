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

// stateSuffix names the marker after the work directory it describes, and
// puts it beside that directory rather than inside it: the instance has no
// use for the file, and several instances on one host share a parent, so a
// plain "restore_state.json" there would have them overwrite each other.
const stateSuffix = ".restore_state.json"

// State is the marker a successful apply leaves next to the work directory.
//
// It is the seam between apply and starting the cluster. A partial restore --
// one node of N skipped or failed -- brings up a cluster whose shards sit on
// different states: each is self-consistent and replication is healthy, so
// nothing looks wrong until the buckets disagree. Comparing the markers of
// every node before the cluster is started is the orchestrator's job; apply
// only leaves the machine-readable evidence it needs.
type State struct {
	SchemaVersion int `json:"schema_version"`
	// WorkDir is the directory this marker describes, so a marker found on
	// its own still says what it belongs to.
	WorkDir string `json:"work_dir"`
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

// StatePath returns the marker path for a work directory.
//
// The directory is resolved first. A relative --work-dir names a directory
// just as well as an absolute one, and appending the suffix to it would put
// the marker somewhere else entirely: "." would name a file inside the
// directory the marker describes, ".." one inside its child.
func StatePath(workDir string) string {
	resolved, err := filepath.Abs(workDir)
	if err != nil {
		// Only a working directory that no longer exists gets here, and the
		// unresolved name is still the one the caller passed.
		resolved = filepath.Clean(workDir)
	}

	return resolved + stateSuffix
}

// writeState writes the marker. It is the last thing a successful apply does,
// so the file's presence means the work directory is complete.
func writeState(state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal restore state: %w", err)
	}

	path := StatePath(state.WorkDir)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("failed to write restore state %q: %w", path, err)
	}

	return nil
}

// removeState drops a previous run's marker. Apply does this before it starts
// rebuilding the work directory, so a run that dies halfway leaves no marker
// claiming the directory is ready.
func removeState(workDir string) error {
	path := StatePath(workDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove stale restore state %q: %w", path, err)
	}

	return nil
}

// ReadState decodes the marker next to workDir.
func ReadState(workDir string) (*State, error) {
	path := StatePath(workDir)

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
