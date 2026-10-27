package daemon

// The test worker in test_process is a separate main package and cannot see
// _test.go files, so the names it shares with TestProcessBase live here.

const (
	StartTestWorkerMsg = "Test worker started"
	StopTestWorkerMsg  = "Test worker stopped"
)

const (
	TestProcessLogPath = "test_process.log"
	TestProcessPidFile = "test_process.pid"
)
