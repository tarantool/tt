package running

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessController(t *testing.T) {
	cmd := exec.Command("bash", "-c", "echo hello")
	out := strings.Builder{}
	cmd.Stdout = &out
	dpc, err := newProcessController(cmd)
	require.NoError(t, err)

	dpc.Wait()
	require.NoError(t, err)
	assert.False(t, dpc.IsAlive())
	assert.Equal(t, "hello\n", out.String())
	err = dpc.Stop(5 * time.Second)
	assert.NoError(t, err) // Already stopped.

	tmpDir := t.TempDir()
	require.NoError(t, copy.Copy("./testdata/signal_handling.py",
		filepath.Join(tmpDir, "signal_handling.py")))

	cmd = exec.Command("python3", filepath.Join(tmpDir, "signal_handling.py"))
	outBuf := bytes.Buffer{}
	cmd.Stdout = &outBuf
	dpc, err = newProcessController(cmd)
	require.NoError(t, err)
	assert.True(t, dpc.IsAlive())

	// Test sending signal
	require.NoError(t, waitForMsgInBuffer(&outBuf, "started", 5*time.Second))
	dpc.SendSignal(syscall.SIGUSR1)
	require.NoError(t, waitForMsgInBuffer(&outBuf, "sigusr1", 5*time.Second))
	assert.True(t, dpc.IsAlive())

	err = dpc.Stop(5 * time.Second)
	var exitError *exec.ExitError
	require.True(t, errors.As(err, &exitError))
	assert.Equal(t, 10, exitError.ProcessState.ExitCode())
	require.NoError(t, waitForMsgInBuffer(&outBuf, "interrupted", 5*time.Second))
	assert.False(t, dpc.IsAlive())

	// Test unknown command.
	cmd = exec.Command("unknown_command")
	dpc, err = newProcessController(cmd)
	require.ErrorContains(t, err, "executable file not found")
	require.Nil(t, dpc)
}
