package running

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

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

	outBuf := bytes.Buffer{}
	cmd = exec.Command("bash", "-c", `trap 'echo sigusr1' SIGUSR1; echo read; read`)
	r, w := io.Pipe()
	cmd.Stdout = &outBuf
	cmd.Stdin = r
	dpc, err = newProcessController(cmd)
	require.NoError(t, err)
	assert.True(t, dpc.IsAlive())

	// Test sending signal
	require.NoError(t, waitForMsgInBuffer(&outBuf, "read", 5*time.Second))
	dpc.SendSignal(syscall.SIGUSR1)
	require.NoError(t, waitForMsgInBuffer(&outBuf, "sigusr1", 5*time.Second))
	assert.True(t, dpc.IsAlive())

	w.Close()
	r.Close()
	err = dpc.Stop(5 * time.Second)
	assert.ErrorContains(t, err, "signal: interrupt")
	assert.False(t, dpc.IsAlive())

	// Test unknown command.
	cmd = exec.Command("unknown_command")
	dpc, err = newProcessController(cmd)
	require.ErrorContains(t, err, "executable file not found")
	require.Nil(t, dpc)
}
