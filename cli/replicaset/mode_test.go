package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
)

func TestMode_String(t *testing.T) {
	cases := []struct {
		Mode     replicaset.Mode
		Expected string
	}{
		{replicaset.ModeUnknown, "unknown"},
		{replicaset.ModeRead, "read"},
		{replicaset.ModeRW, "rw"},
		{replicaset.Mode(123), "Mode(123)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Mode.String())
		})
	}
}
