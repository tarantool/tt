package running

import "time"

// waitProcessStart waits for the new process to set signal handlers.
func waitProcessStart() {
	// We need to wait for the new process (tarantool instance) to set handlers.
	// It is necessary to update for more correct synchronization.
	time.Sleep(1000 * time.Millisecond)
}
