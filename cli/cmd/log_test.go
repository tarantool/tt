package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/running"
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
			LogDir:    filepath.Join("var", "log"),
			SingleApp: true,
		}
		actual := logRoot(inst)
		require.Equal(t, inst.LogDir, actual)
	})
}

func TestMonitorLogRoot(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
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
