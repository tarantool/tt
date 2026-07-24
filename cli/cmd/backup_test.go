package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

func TestParseFromVclock_empty(t *testing.T) {
	vc, err := parseFromVclock("")
	require.NoError(t, err)
	assert.Nil(t, vc, "empty flag means a full backup")
}

func TestParseFromVclock_json(t *testing.T) {
	vc, err := parseFromVclock(`{"1":1500,"2":230}`)
	require.NoError(t, err)
	assert.Equal(t, map[uint32]uint64{1: 1500, 2: 230}, map[uint32]uint64(vc))
}

func TestParseFromVclock_invalid(t *testing.T) {
	_, err := parseFromVclock("not-json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --from-vclock")
}

func TestInstanceNameFromTarget(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"app:router-001", "router-001"},
		{"app:", ""},
		{"localhost:3301", "3301"},
		{"suffix", ""},
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			assert.Equal(t, tc.want, instanceNameFromTarget(tc.target))
		})
	}
}

func TestRunBackupLast_Filesystem(t *testing.T) {
	// Create a temporary directory with manifest files.
	tmpDir := t.TempDir()

	// Write two manifests: a full backup and an incremental.
	fullManifest := backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         backup.BackupID("2024-01-01-full"),
		PreviousBackupID: "",
		BaseFullBackupID: "2024-01-01-full",
		Status:           backup.StatusOK,
		CreationTime:     time.Unix(1000, 0).UTC(),
		CreationDuration: time.Second,
		Shards: map[string]backup.Shard{
			"rs-1": {
				Instance: &backup.ShardInstance{
					InstanceUUID: "inst-1",
					InstanceName: "inst-1",
					Hostname:     "localhost",
					VclockBegin:  backup.Vclock{1: 0},
					VclockEnd:    backup.Vclock{1: 100},
					Artifact: backup.Artifact{
						Path:           "data/2024-01-01-full-rs-1.tar.zst",
						SizeBytes:      1024,
						ChecksumSHA256: strings.Repeat("a", 64),
						Compression:    "zstd",
						Files:          []string{"00000000000000000000.xlog"},
						Type:           backup.BackupTypeFull,
					},
				},
			},
		},
		Topology: backup.Topology{Replicasets: map[string][]backup.TopologyInstance{
			"rs-1": {{InstanceUUID: "inst-1", InstanceName: "inst-1"}},
		}},
		Warnings: []backup.Warning{},
	}

	incrementalManifest := backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         backup.BackupID("2024-01-02-inc"),
		PreviousBackupID: "2024-01-01-full",
		BaseFullBackupID: "2024-01-01-full",
		Status:           backup.StatusOK,
		CreationTime:     time.Unix(2000, 0).UTC(),
		CreationDuration: time.Second,
		Shards: map[string]backup.Shard{
			"rs-1": {
				Instance: &backup.ShardInstance{
					InstanceUUID: "inst-1",
					InstanceName: "inst-1",
					Hostname:     "localhost",
					VclockBegin:  backup.Vclock{1: 100},
					VclockEnd:    backup.Vclock{1: 200},
					Artifact: backup.Artifact{
						Path:           "data/2024-01-02-inc-rs-1.tar.zst",
						SizeBytes:      512,
						ChecksumSHA256: strings.Repeat("b", 64),
						Compression:    "zstd",
						Files:          []string{"00000000000000000000.xlog"},
						Type:           backup.BackupTypeIncremental,
					},
				},
			},
		},
		Topology: backup.Topology{Replicasets: map[string][]backup.TopologyInstance{
			"rs-1": {{InstanceUUID: "inst-1", InstanceName: "inst-1"}},
		}},
		Warnings: []backup.Warning{},
	}

	// Write manifests to the filesystem.
	for _, m := range []backup.ClusterManifest{fullManifest, incrementalManifest} {
		data, err := json.Marshal(m)
		require.NoError(t, err)
		key := storage.ManifestKey(string(m.BackupID))
		fullPath := filepath.Join(tmpDir, filepath.FromSlash(key))
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		require.NoError(t, os.WriteFile(fullPath, data, 0o644))
	}

	// Parse the file:// URI and open storage.
	uri := "file://" + tmpDir
	cfg, err := backup.ParseStorageURI(uri)
	require.NoError(t, err)
	store, err := backup.OpenStorage(cfg)
	require.NoError(t, err)

	// Load the chain and get the latest entry.
	ch, err := chain.Load(context.Background(), store)
	require.NoError(t, err)

	entry := ch.Latest()
	require.NotNil(t, entry)
	assert.Equal(t, backup.BackupID("2024-01-02-inc"), entry.Manifest.BackupID)
	assert.Equal(t, backup.BackupID("2024-01-01-full"), entry.Manifest.PreviousBackupID)
}

func TestRunBackupLast_EmptyStorage(t *testing.T) {
	tmpDir := t.TempDir()

	uri := "file://" + tmpDir
	cfg, err := backup.ParseStorageURI(uri)
	require.NoError(t, err)
	store, err := backup.OpenStorage(cfg)
	require.NoError(t, err)

	ch, err := chain.Load(context.Background(), store)
	require.NoError(t, err)

	entry := ch.Latest()
	assert.Nil(t, entry, "empty storage should have no latest entry")
}
