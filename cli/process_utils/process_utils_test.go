package process_utils

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ExistsAndRecord(t *testing.T) {
	testFile := "test.pid"
	invalid := "invalid.pid"
	cmd := exec.CommandContext(t.Context(), "sleep", "10")

	t.Cleanup(func() {
		_ = os.Remove(testFile)
	})

	err := cmd.Start()
	require.NoError(t, err)

	err = CreatePIDFile(testFile, cmd.Process.Pid)
	require.NoError(t, err)

	status, err := ExistsAndRecord(testFile)
	require.NoError(t, err)
	require.True(t, status)

	err = cmd.Process.Kill()
	require.NoError(t, err)
	require.Error(t, cmd.Wait())

	statusInvalid, err := ExistsAndRecord(invalid)
	require.False(t, statusInvalid)
	require.NoError(t, err)
}
