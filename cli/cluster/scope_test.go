package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitInstancePath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantG     string
		wantR     string
		wantI     string
		wantEmpty bool
	}{
		{
			name:  "valid path",
			path:  "groups/g1/replicasets/r1/instances/i1",
			wantG: "g1",
			wantR: "r1",
			wantI: "i1",
		},
		{
			name:      "empty path",
			path:      "",
			wantEmpty: true,
		},
		{
			name:      "too short",
			path:      "groups/g1/replicasets/r1",
			wantEmpty: true,
		},
		{
			name:      "wrong first segment",
			path:      "GROUPS/g1/replicasets/r1/instances/i1",
			wantEmpty: true,
		},
		{
			name:      "wrong middle segment",
			path:      "groups/g1/REPLICASETS/r1/instances/i1",
			wantEmpty: true,
		},
		{
			name:      "wrong last structural segment",
			path:      "groups/g1/replicasets/r1/INSTANCES/i1",
			wantEmpty: true,
		},
		{
			name:      "extra segments",
			path:      "groups/g1/replicasets/r1/instances/i1/extra",
			wantEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, r, i := splitInstancePath(tc.path)
			if tc.wantEmpty {
				assert.Empty(t, g)
				assert.Empty(t, r)
				assert.Empty(t, i)
			} else {
				assert.Equal(t, tc.wantG, g)
				assert.Equal(t, tc.wantR, r)
				assert.Equal(t, tc.wantI, i)
			}
		})
	}
}
