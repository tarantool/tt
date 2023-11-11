package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
)

func TestMaster_String(t *testing.T) {
	cases := []struct {
		Master   replicaset.Master
		Expected string
	}{
		{replicaset.MasterUnknown, "unknown"},
		{replicaset.MasterNo, "no"},
		{replicaset.MasterSingle, "single"},
		{replicaset.MasterMulti, "multi"},
		{replicaset.Master(123), "Master(123)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Master.String())
		})
	}
}
