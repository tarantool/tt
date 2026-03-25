package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
)

func TestFailover_String(t *testing.T) {
	cases := []struct {
		Failover replicaset.Failover
		Expected string
	}{
		{replicaset.FailoverUnknown, "unknown"},
		{replicaset.FailoverOff, "off"},
		{replicaset.FailoverManual, "manual"},
		{replicaset.FailoverElection, "election"},
		{replicaset.FailoverSupervised, "supervised"},
		{replicaset.Failover(123), "Failover(123)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Failover.String())
		})
	}
}

func TestParseFailover(t *testing.T) {
	cases := []struct {
		String   string
		Expected replicaset.Failover
	}{
		{"off", replicaset.FailoverOff},
		{"OFf", replicaset.FailoverOff},
		{"disabled", replicaset.FailoverOff},
		{"DISABLED", replicaset.FailoverOff},
		{"manual", replicaset.FailoverManual},
		{"maNUAl", replicaset.FailoverManual},
		{"election", replicaset.FailoverElection},
		{"eLECTION", replicaset.FailoverElection},
		{"raft", replicaset.FailoverElection},
		{"RAft", replicaset.FailoverElection},
		{"supervised", replicaset.FailoverSupervised},
		{"SUPERvised", replicaset.FailoverSupervised},
		{"unknown", replicaset.FailoverUnknown},
		{"foo", replicaset.FailoverUnknown},
		{"offfoo", replicaset.FailoverUnknown}, // spell-checker:disable-line
	}

	for _, tc := range cases {
		t.Run(tc.String, func(t *testing.T) {
			assert.Equal(t, tc.Expected, replicaset.ParseFailover(tc.String))
		})
	}
}
