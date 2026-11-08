package cmd

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPackageContextCancelsOnSignal pins what keeps a build tool from
// outliving tt: SIGINT and SIGTERM cancel the context of a package command,
// which the native backend kills the tool's process group on.
func TestPackageContextCancelsOnSignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		ctx, stop := packageContext()

		require.NoError(t, syscall.Kill(syscall.Getpid(), sig))

		select {
		case <-ctx.Done():
			require.ErrorIs(t, ctx.Err(), context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatalf("%v did not cancel the context", sig)
		}

		stop()
	}
}
