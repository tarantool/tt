package running

import (
	"os"
	"time"
)

// waitForFile waits for the file to appear.
func waitForFile(filePath string) int {
	retries := 10
	for retries > 0 {
		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(filePath); err == nil {
			break
		}
		retries--
	}
	return retries
}
