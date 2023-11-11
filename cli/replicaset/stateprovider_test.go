package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
)

func TestStateProvider_String(t *testing.T) {
	cases := []struct {
		StateProvider replicaset.StateProvider
		Expected      string
	}{
		{replicaset.StateProviderUnknown, "unknown"},
		{replicaset.StateProviderNone, "none"},
		{replicaset.StateProviderTarantool, "tarantool"},
		{replicaset.StateProviderEtcd2, "etcd2"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.StateProvider.String())
		})
	}
}

func TestParseStateProvider(t *testing.T) {
	cases := []struct {
		String   string
		Expected replicaset.StateProvider
	}{
		{"none", replicaset.StateProviderNone},
		{"NONE", replicaset.StateProviderNone},
		{"tarantool", replicaset.StateProviderTarantool},
		{"taRANTOOL", replicaset.StateProviderTarantool},
		{"etcd2", replicaset.StateProviderEtcd2},
		{"ETCD2", replicaset.StateProviderEtcd2},
		{"foo", replicaset.StateProviderUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.String, func(t *testing.T) {
			assert.Equal(t, tc.Expected, replicaset.ParseStateProvider(tc.String))
		})
	}
}
