package process_utils_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/process_utils"
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

	owned, err := process_utils.CreatePIDFile(testFile, cmd.Process.Pid)
	require.NoError(t, err)
	require.NoError(t, owned.Keep())

	status, err := process_utils.ExistsAndRecord(testFile)
	require.NoError(t, err)
	require.True(t, status)

	err = cmd.Process.Kill()
	require.NoError(t, err)
	require.Error(t, cmd.Wait())

	statusInvalid, err := process_utils.ExistsAndRecord(invalid)
	require.False(t, statusInvalid)
	require.NoError(t, err)
}
