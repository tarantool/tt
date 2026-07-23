package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
