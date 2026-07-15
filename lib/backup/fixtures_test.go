package backup

import (
	_ "embed"
	"encoding/json"
	"testing"

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

	var manifest ClusterManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	return manifest
}

func mustDecodeFragment(t *testing.T, data []byte) Fragment {
	t.Helper()

	var fragment Fragment
	require.NoError(t, json.Unmarshal(data, &fragment))
	return fragment
}
