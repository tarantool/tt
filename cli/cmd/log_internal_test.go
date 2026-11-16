package cmd

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
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

func TestPrintLinesEndsOnInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan struct{})

	go func() {
		// Nothing is ever sent: only the cancelled context ends it.
		printLines(ctx, make(chan string))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		require.Fail(t, "an interrupt did not end printing the lines")
	}
}

func TestFollowInterruptedIsNoError(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "log", "inst", "tt.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(logFile), 0o755))
	require.NoError(t, os.WriteFile(logFile, []byte("line\n"), 0o644))

	inst := running.InstanceCtx{
		AppName:  "app",
		InstName: "inst",
		LogDir:   filepath.Dir(logFile),
		Log:      logFile,
	}

	// The SIGINTs below must not end the test binary before follow listens.
	sigs := make(chan os.Signal, 16)
	signal.Notify(sigs, os.Interrupt)

	defer signal.Stop(sigs)

	errCh := make(chan error, 1)

	go func() { errCh <- follow([]running.InstanceCtx{inst}, 1) }()

	// follow listens for SIGINT once it has started: send it until it ends.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	deadline := time.After(5 * time.Second)

	for {
		select {
		case err := <-errCh:
			require.NoError(t, err)

			return
		case <-ticker.C:
			require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
		case <-deadline:
			require.Fail(t, "follow did not end on SIGINT")

			return
		}
	}
}
