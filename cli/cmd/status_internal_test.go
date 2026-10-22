package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/status"
)

func TestStatusInstanceTimeoutFlag(t *testing.T) {
	previous := opts

	t.Cleanup(func() { opts = previous })

	cmd := NewStatusCmd()

	require.Equal(t, status.DefaultInstanceTimeout, opts.instanceTimeout)
	require.NoError(t, cmd.Flags().Parse([]string{"--instance-timeout", "0"}))
	require.Zero(t, opts.instanceTimeout)
	require.NoError(t, cmd.Flags().Parse([]string{"--instance-timeout", "250ms"}))
	require.Equal(t, 250*time.Millisecond, opts.instanceTimeout)
	require.NoError(t, cmd.Flags().Parse([]string{"--instance-timeout", "-1s"}))
	require.EqualError(t, internalStatusModule(&cmdcontext.CmdCtx{}, nil),
		"instance timeout must not be negative")
}
