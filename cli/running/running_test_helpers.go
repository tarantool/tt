package running

import (
	"os"
	"time"
)

const filePollInterval = 500 * time.Millisecond

// waitForFile waits for the file to appear.
func waitForFile(filePath string) int {
	retries := 10
	for retries > 0 {
		time.Sleep(filePollInterval)
		if _, err := os.Stat(filePath); err == nil {
			break
		}
		retries--
	}
	return retries
}
