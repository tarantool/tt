package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/replicaset"
)

func TestElectionMode_String(t *testing.T) {
	cases := []struct {
		ElectionMode replicaset.ElectionMode
		Expected     string
	}{
		{replicaset.ElectionModeUnknown, "unknown"},
		{replicaset.ElectionModeOff, "off"},
		{replicaset.ElectionModeVoter, "voter"},
		{replicaset.ElectionModeCandidate, "candidate"},
		{replicaset.ElectionModeManual, "manual"},
		{replicaset.ElectionMode(42), "ElectionMode(42)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.ElectionMode.String())
		})
	}
}

func TestParseElectionMode(t *testing.T) {
	cases := []struct {
		String   string
		Expected replicaset.ElectionMode
	}{
		{"off", replicaset.ElectionModeOff},
		{"OfF", replicaset.ElectionModeOff},
		{"voter", replicaset.ElectionModeVoter},
		{"VOTeR", replicaset.ElectionModeVoter},
		{"candidate", replicaset.ElectionModeCandidate},
		{"CaNdIdAtE", replicaset.ElectionModeCandidate},
		{"manual", replicaset.ElectionModeManual},
		{"maNUAL", replicaset.ElectionModeManual},
		{"unknown", replicaset.ElectionModeUnknown},
		{"curiosity", replicaset.ElectionModeUnknown},
		{"offfoo", replicaset.ElectionModeUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.String, func(t *testing.T) {
			assert.Equal(t, tc.Expected, replicaset.ParseElectionMode(tc.String))
		})
	}
}
