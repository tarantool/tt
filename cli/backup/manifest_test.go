package backup

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first full backup of a chain has no predecessor. The spec types that as
// null and its examples carry null, so a consumer branching on `=== null` has
// to see null -- and a manifest written to the spec has to read back the same
// way, which is what an external tool producing manifests relies on.
func TestOptionalBackupIDEncoding(t *testing.T) {
	t.Run("an absent id is written as null", func(t *testing.T) {
		data, err := json.Marshal(struct {
			Previous OptionalBackupID `json:"previous_backup_id"`
		}{})
		require.NoError(t, err)
		assert.JSONEq(t, `{"previous_backup_id":null}`, string(data))
	})

	t.Run("a present id is written as a string", func(t *testing.T) {
		data, err := json.Marshal(struct {
			Previous OptionalBackupID `json:"previous_backup_id"`
		}{Previous: "bk-1"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"previous_backup_id":"bk-1"}`, string(data))
	})

	cases := map[string]struct {
		json string
		want OptionalBackupID
	}{
		"null":        {json: `{"previous_backup_id":null}`},
		"missing key": {json: `{}`},
		"an id":       {json: `{"previous_backup_id":"bk-1"}`, want: "bk-1"},
		// Manifests written before the field became nullable say "", and a
		// storage full of them still has to be readable.
		"empty string": {json: `{"previous_backup_id":""}`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var decoded struct {
				Previous OptionalBackupID `json:"previous_backup_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(tc.json), &decoded))
			assert.Equal(t, tc.want, decoded.Previous)
		})
	}
}

// A manifest written to the spec -- null, not "" -- survives a decode and an
// encode unchanged. This is the round trip an external producer depends on;
// the fixture is the RFC's own 5.2.2 example.
func TestClusterManifestRoundTripsANullPrevious(t *testing.T) {
	var manifest ClusterManifest
	require.NoError(t, json.Unmarshal(fixtureClusterManifest, &manifest))
	assert.Empty(t, manifest.PreviousBackupID)

	// A manifest with no predecessor is a valid manifest: last and verify read
	// it through Validate before they print or check anything.
	require.NoError(t, manifest.Validate())

	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"previous_backup_id":null`)

	// base_full_backup_id is required and never absent: a full backup is the
	// base of its own chain. It must not follow previous into being nullable.
	assert.Contains(t, string(data), `"base_full_backup_id":"20260312T120000Z"`)
}
