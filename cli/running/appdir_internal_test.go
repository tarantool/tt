package running

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/pidfile"
)

// TestRecordAppDir pins the record of the application directory a watchdog
// keeps next to its pid file: it is replaced whole, read only while the pid
// file is held, and removed as the watchdog ends.
func TestRecordAppDir(t *testing.T) {
	runDir := t.TempDir()
	inst := InstanceCtx{
		AppDir:  "/root/versions/myapp/myapp_1.0.0",
		RunDir:  runDir,
		PIDFile: filepath.Join(runDir, "tt.pid"),
	}

	require.NoError(t, recordAppDir(&inst))

	_, known := RunningAppDir(&inst)
	assert.False(t, known, "a record without a watchdog holding the pid file tells nothing")

	owned, err := pidfile.Acquire(inst.PIDFile, os.Getpid())
	require.NoError(t, err)
	t.Cleanup(func() { _ = owned.Release() })

	dir, known := RunningAppDir(&inst)
	assert.True(t, known)
	assert.Equal(t, "/root/versions/myapp/myapp_1.0.0", dir)

	inst.AppDir = "/root/versions/myapp/myapp_2.0.0"
	require.NoError(t, recordAppDir(&inst))

	dir, known = RunningAppDir(&inst)
	assert.True(t, known)
	assert.Equal(t, "/root/versions/myapp/myapp_2.0.0", dir)

	entries, err := os.ReadDir(runDir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	assert.ElementsMatch(t, []string{"app.dir", "tt.pid"}, names, "no temporary file is left")

	require.NoError(t, removeAppDir(&inst))
	require.NoError(t, removeAppDir(&inst), "removing no record is no error")

	_, known = RunningAppDir(&inst)
	assert.False(t, known)
}
