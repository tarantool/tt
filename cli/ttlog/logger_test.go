package ttlog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// cleanupLog clean all log files with the temporary directory.
func cleanupLog(logDir string) {
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		return
	}
	os.RemoveAll(logDir)
	return
}

func TestLoggerBase(t *testing.T) {
	assert := assert.New(t)

	// Create a temporary directory for the log files.
	dir, err := os.MkdirTemp("", "test_dir")
	assert.Nil(err, `Can't create a temporary directory: "%v".`, err)
	defer os.RemoveAll(dir)
	t.Cleanup(func() { cleanupLog(dir) })

	fileName := filepath.Join(dir, "test_log")

	// Create logger.
	opts := LoggerOpts{
		Filename:   fileName,
		MaxSize:    5,
		MaxBackups: 5,
		MaxAge:     1,
	}
	logger := NewLogger(&opts)

	// Write one test message to create a log file.
	logger.Printf(`Test msg`)

	// Check the count of the log files (must be 1).
	files, err := os.ReadDir(dir)
	assert.Equal(len(files), 1)

	logger.Rotate()

	// Check the count of the log files after rotation (must be 2).
	files, err = os.ReadDir(dir)
	assert.Equal(len(files), 2)
}
