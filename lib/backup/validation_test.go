package backup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testBackupID BackupID = "20260312T120000Z"
	testRSA      string   = "11111111-1111-1111-1111-111111111111"
	testRSB      string   = "22222222-2222-2222-2222-222222222222"
	testRSC      string   = "33333333-3333-3333-3333-333333333333"
)

func TestFragmentValidate(t *testing.T) {
	valid := mustDecodeFragment(t, fixtureFragmentA)

	tests := []struct {
		name      string
		fragment  Fragment
		wantError string
	}{
		{
			name:     "valid",
			fragment: valid,
		},
		{
			name: "empty replicaset uuid",
			fragment: func() Fragment {
				fragment := valid
				fragment.ReplicasetUUID = ""
				return fragment
			}(),
			wantError: "replicaset_uuid is empty",
		},
		{
			name: "empty instance uuid",
			fragment: func() Fragment {
				fragment := valid
				fragment.InstanceUUID = ""
				return fragment
			}(),
			wantError: "instance_uuid is empty",
		},
		{
			name: "invalid type",
			fragment: func() Fragment {
				fragment := valid
				fragment.Type = BackupType("snapshot")
				return fragment
			}(),
			wantError: "invalid backup type",
		},
		{
			name: "empty vclock begin",
			fragment: func() Fragment {
				fragment := valid
				fragment.VclockBegin = nil
				return fragment
			}(),
			wantError: "vclock_begin is empty",
		},
		{
			name: "empty vclock end",
			fragment: func() Fragment {
				fragment := valid
				fragment.VclockEnd = Vclock{}
				return fragment
			}(),
			wantError: "vclock_end is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fragment.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
		})
	}
}

func TestClusterManifestValidate(t *testing.T) {
	valid := func() ClusterManifest { return testClusterManifest(t, StatusOK) }

	tests := []struct {
		name      string
		manifest  ClusterManifest
		wantError string
	}{
		{
			name:     "valid",
			manifest: valid(),
		},
		{
			name: "foreign schema version",
			manifest: func() ClusterManifest {
				manifest := valid()
				manifest.SchemaVersion = 2
				return manifest
			}(),
			wantError: "unsupported schema_version 2",
		},
		{
			name: "empty backup id",
			manifest: func() ClusterManifest {
				manifest := valid()
				manifest.BackupID = ""
				return manifest
			}(),
			wantError: "backup_id is empty",
		},
		{
			name: "invalid status",
			manifest: func() ClusterManifest {
				manifest := valid()
				manifest.Status = Status("partial")
				return manifest
			}(),
			wantError: "invalid status",
		},
		{
			name: "shard has both instance and error",
			manifest: func() ClusterManifest {
				manifest := valid()
				shard := manifest.Shards[testRSA]
				shard.Error = "unexpected error"
				manifest.Shards[testRSA] = shard
				return manifest
			}(),
			wantError: "must contain exactly one of instance or error",
		},
		{
			name: "shard has neither instance nor error",
			manifest: func() ClusterManifest {
				manifest := valid()
				manifest.Shards[testRSA] = Shard{}
				return manifest
			}(),
			wantError: "must contain exactly one of instance or error",
		},
		{
			name: "orphan shard outside topology",
			manifest: func() ClusterManifest {
				manifest := valid()
				manifest.Shards["99999999-9999-9999-9999-999999999999"] = manifest.Shards[testRSA]
				return manifest
			}(),
			wantError: "is not present in topology",
		},
		{
			name: "successful shard with empty vclock begin",
			manifest: func() ClusterManifest {
				manifest := valid()
				shard := manifest.Shards[testRSA]
				shard.Instance.VclockBegin = nil
				manifest.Shards[testRSA] = shard
				return manifest
			}(),
			wantError: "vclock_begin is empty",
		},
		{
			name: "invalid artifact type",
			manifest: func() ClusterManifest {
				manifest := valid()
				shard := manifest.Shards[testRSA]
				shard.Instance.Artifact.Type = BackupType("bad")
				manifest.Shards[testRSA] = shard
				return manifest
			}(),
			wantError: "invalid backup type",
		},
		{
			name: "invalid warning code",
			manifest: func() ClusterManifest {
				manifest := valid()
				manifest.Warnings = []Warning{{Code: WarningCode("unknown")}}
				return manifest
			}(),
			wantError: "invalid code",
		},
		{
			name: "nil warnings",
			manifest: func() ClusterManifest {
				manifest := valid()
				manifest.Warnings = nil
				return manifest
			}(),
			wantError: "warnings is nil",
		},
		{
			name: "nil recovery points",
			manifest: func() ClusterManifest {
				manifest := valid()
				shard := manifest.Shards[testRSA]
				shard.Instance.Artifact.RecoveryPoints = nil
				manifest.Shards[testRSA] = shard
				return manifest
			}(),
			wantError: "artifact.recovery_points is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.manifest.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
		})
	}
}

func TestClusterManifestIsValidAndSerializable(t *testing.T) {
	manifest := mustDecodeClusterManifest(t, fixtureClusterManifest)
	require.NoError(t, manifest.Validate())

	data, err := json.Marshal(manifest)
	require.NoError(t, err)

	jsonText := string(data)
	for _, want := range []string{
		`"schema_version":1`,
		`"previous_backup_id":""`,
		`"creation_duration":2300000000`,
		`"status":"OK"`,
		`"550e8400-e29b-41d4-a716-446655440000"`,
		`"6ba7b810-9dad-11d1-80b4-00c04fd430c8"`,
		`"error":"timeout: replicaset unreachable"`,
		`"code":"shard_unreachable"`,
	} {
		require.Contains(t, jsonText, want)
	}

	type clusterManifestRoundTrip struct {
		SchemaVersion    int              `json:"schema_version"`
		BackupID         BackupID         `json:"backup_id"`
		PreviousBackupID BackupID         `json:"previous_backup_id"`
		BaseFullBackupID BackupID         `json:"base_full_backup_id"`
		Status           Status           `json:"status"`
		CreationTime     time.Time        `json:"creation_time"`
		CreationDuration time.Duration    `json:"creation_duration"`
		Shards           map[string]Shard `json:"shards"`
		Topology         Topology         `json:"topology"`
		Warnings         []Warning        `json:"warnings"`
	}

	var roundTrip clusterManifestRoundTrip
	require.NoError(t, json.Unmarshal(data, &roundTrip))
	decoded := ClusterManifest(roundTrip)
	require.NoError(t, decoded.Validate())
	require.Equal(t, 2300*time.Millisecond, decoded.CreationDuration)
}

func testClusterManifest(t *testing.T, status Status) ClusterManifest {
	t.Helper()

	manifest := mustDecodeClusterManifest(t, fixtureClusterManifest)
	manifest.Status = status
	delete(manifest.Shards, testRSB)
	delete(manifest.Shards, testRSC)
	delete(manifest.Topology.Replicasets, testRSB)
	delete(manifest.Topology.Replicasets, testRSC)
	manifest.Warnings = []Warning{}
	return manifest
}
