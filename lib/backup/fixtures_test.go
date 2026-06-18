package backup

import (
	_ "embed"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	//go:embed testdata/cluster_manifest.json
	fixtureClusterManifest []byte
	//go:embed testdata/fragment_a.json
	fixtureFragmentA []byte
	//go:embed testdata/fragment_b.json
	fixtureFragmentB []byte
	//go:embed testdata/fragment_without_recovery_points.json
	fixtureFragmentWithoutRecoveryPoints []byte
	//go:embed testdata/fragment_with_empty_recovery_points.json
	fixtureFragmentWithEmptyRecoveryPoints []byte
)

func mustDecodeClusterManifest(t *testing.T, data []byte) ClusterManifest {
	t.Helper()

	type clusterManifestJSON struct {
		SchemaVersion    int              `json:"schema_version"`
		BackupID         BackupID         `json:"backup_id"`
		PreviousBackupID BackupID         `json:"previous_backup_id"`
		BaseFullBackupID BackupID         `json:"base_full_backup_id"`
		Status           Status           `json:"status"`
		CreationTime     time.Time        `json:"creation_time"`
		CreationDuration string           `json:"creation_duration"`
		Shards           map[string]Shard `json:"shards"`
		Topology         Topology         `json:"topology"`
		Warnings         []Warning        `json:"warnings"`
	}

	var raw clusterManifestJSON
	require.NoError(t, json.Unmarshal(data, &raw))
	duration, err := time.ParseDuration(raw.CreationDuration)
	require.NoError(t, err)
	return ClusterManifest{
		SchemaVersion:    raw.SchemaVersion,
		BackupID:         raw.BackupID,
		PreviousBackupID: raw.PreviousBackupID,
		BaseFullBackupID: raw.BaseFullBackupID,
		Status:           raw.Status,
		CreationTime:     raw.CreationTime,
		CreationDuration: duration,
		Shards:           raw.Shards,
		Topology:         raw.Topology,
		Warnings:         raw.Warnings,
	}
}

func mustDecodeFragment(t *testing.T, data []byte) Fragment {
	t.Helper()

	var fragment Fragment
	require.NoError(t, json.Unmarshal(data, &fragment))
	return fragment
}
