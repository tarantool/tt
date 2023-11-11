package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
)

func TestState_String(t *testing.T) {
	cases := []struct {
		State    replicaset.State
		Expected string
	}{
		{replicaset.StateUnknown, "unknown"},
		{replicaset.StateUninitialized, "uninitialized"},
		{replicaset.StateBootstrapped, "bootstrapped"},
		{replicaset.State(123), "State(123)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.State.String())
		})
	}
}
