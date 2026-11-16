package ttlog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/ttlog"
)

func TestLoggerBase(t *testing.T) {
	// Create a temporary directory for the log files.
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "test_log")

	// Create logger.
	opts := ttlog.LoggerOpts{Filename: fileName, Prefix: "watchdog "}
	logger, err := ttlog.NewFileLogger(opts)
	require.NoError(t, err)
	// Write one test message.
	logger.Println(`Test msg 1`)

	// Check the count of the log files (must be 1).
	assert.FileExists(t, fileName)

	_ = logger.Rotate()

	// Check that the rotation does not create new file.
	files, _ := os.ReadDir(tmpDir)
	assert.Len(t, files, 1)

	_ = os.Rename(fileName, fileName+".old")
	assert.NoFileExists(t, fileName)
	logger.Println(`Test msg 2`)

	_ = logger.Rotate()

	// Check that file is re-created.
	assert.FileExists(t, fileName)
	assert.FileExists(t, fileName+".old")

	logger.Println(`Test msg 3`)
	require.NoError(t, logger.Close())

	content, err := os.ReadFile(fileName + ".old")
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "watchdog")
	assert.Contains(t, contentStr, "Test msg 1")
	assert.Contains(t, contentStr, "Test msg 2")
	assert.Contains(t, contentStr, "log file has been reopened")

	content, err = os.ReadFile(fileName)
	require.NoError(t, err)

	contentStr = string(content)
	assert.Contains(t, contentStr, "Test msg 3")
	assert.Contains(t, contentStr, "log file has been reopened")
}

func TestCheckLogFile(t *testing.T) {
	dir := t.TempDir()
	fileName := filepath.Join(dir, "tt.log")

	// A log that is not there is created, as the logger creates it.
	require.NoError(t, ttlog.CheckLogFile(fileName))
	require.FileExists(t, fileName)

	if os.Geteuid() == 0 {
		t.Skip("root writes a file whatever its mode")
	}

	require.NoError(t, os.Chmod(fileName, 0o440))

	err := ttlog.CheckLogFile(fileName)
	require.ErrorIs(t, err, os.ErrPermission)
	require.ErrorContains(t, err, "cannot open the log file")

	require.NoError(t, os.Remove(fileName))
	require.NoError(t, os.Chmod(dir, 0o550))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	require.ErrorIs(t, ttlog.CheckLogFile(fileName), os.ErrPermission)
}

func TestLoggerNoDir(t *testing.T) {
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "dir", "subdir", "test.log")

	opts := ttlog.LoggerOpts{Filename: fileName, Prefix: "watchdog "}
	logger, err := ttlog.NewFileLogger(opts)
	require.NoError(t, err)
	logger.Println(`Test msg 1`)

	require.FileExists(t, fileName)
	require.NoError(t, logger.Close())

	content, err := os.ReadFile(fileName)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "watchdog")
	assert.Contains(t, contentStr, "Test msg 1")
}
