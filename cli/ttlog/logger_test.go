package ttlog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggerBase(t *testing.T) {
	// Create a temporary directory for the log files.
	tmpDir := t.TempDir()
	fileName := filepath.Join(tmpDir, "test_log")

	// Create logger.
	opts := LoggerOpts{fileName, "watchdog"}
	logger := NewLogger(opts)
	// Write one test message to create a log file.
	logger.Println(`Test msg 1`)

	// Check the count of the log files (must be 1).
	assert.FileExists(t, fileName)

	logger.Rotate()

	// Check that the rotation does not create new file.
	files, _ := os.ReadDir(tmpDir)
	assert.Equal(t, len(files), 1)

	os.Rename(fileName, fileName+".old")
	assert.NoFileExists(t, fileName)
	logger.Println(`Test msg 2`)
	logger.Rotate()

	// Check that file is re-created.
	assert.FileExists(t, fileName)
	assert.FileExists(t, fileName+".old")

	logger.Println(`Test msg 3`)
	assert.NoError(t, logger.Close())

	content, err := os.ReadFile(fileName + ".old")
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "watchdog")
	assert.Contains(t, contentStr, "Test msg 1")
	assert.Contains(t, contentStr, "Test msg 2")

	content, err = os.ReadFile(fileName)
	require.NoError(t, err)
	contentStr = string(content)
	assert.Contains(t, contentStr, "Test msg 3")
}
