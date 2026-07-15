package chain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusEncoding(t *testing.T) {
	tests := []struct {
		status Status
		name   string
	}{
		{StatusOK, "ok"},
		{StatusTopologyBoundary, "topology_boundary"},
		{StatusNoRecoveryPoint, "no_recovery_point"},
		{StatusChainBroken, "chain_broken"},
		{StatusOutOfRange, "out_of_range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.name, tt.status.String())
			data, err := json.Marshal(tt.status)
			require.NoError(t, err)
			require.JSONEq(t, `"`+tt.name+`"`, string(data))
		})
	}
}
