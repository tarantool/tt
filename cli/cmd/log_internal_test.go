package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/running"
)

func TestLogRoot(t *testing.T) {
	t.Run("multi-instance application", func(t *testing.T) {
		inst := running.InstanceCtx{
			LogDir: filepath.Join("app", "var", "log", "instance"),
		}
		expected := filepath.Join("app", "var", "log")
		actual := logRoot(inst)
		require.Equal(t, expected, actual)
	})

	t.Run("single-instance application", func(t *testing.T) {
		inst := running.InstanceCtx{
			LogDir: filepath.Join(".tarantool", "var", "log", "app", "instance"),
		}
		actual := logRoot(inst)
		require.Equal(t, filepath.Join(".tarantool", "var", "log", "app"), actual)
	})
}

func TestMonitorLogRoot(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())

	defer cancel()

	go monitorLogRoot(ctx, root, cancel)

	err := os.Remove(root)
	require.NoError(t, err)

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		require.Fail(t, "log root removal did not cancel the follow context")
	}
}
