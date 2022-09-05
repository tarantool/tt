package main

import (
	"os"

	"github.com/tarantool/tt/cli/daemon"
	"github.com/tarantool/tt/cli/ttlog"
)

// TestWorker represents simple worker.
type TestWorker struct {
	logger *ttlog.Logger
	done   chan bool
}

// NewTestWorker creates new TestWorker.
func NewTestWorker() *TestWorker {
	return &TestWorker{
		done: make(chan bool, 1),
	}
}

// SetLogger sets worker logger.
func (w *TestWorker) SetLogger(logger *ttlog.Logger) {
	w.logger = logger
}

// Start starts worker.
func (w *TestWorker) Start(ttPath string) {
	w.logger.Println(daemon.StartTestWorkerMsg)

	go func() {
		for {
			select {
			case <-w.done:
				return
			}
		}
	}()
}

// Stop stops worker.
func (w *TestWorker) Stop() error {
	w.logger.Println(daemon.StopTestWorkerMsg)

	w.done <- true
	return nil
}

func main() {
	logOpts := ttlog.LoggerOpts{
		Filename:   daemon.TestProcessLogPath,
		MaxSize:    0,
		MaxBackups: 0,
		MaxAge:     0,
	}

	proc := daemon.NewProcess(NewTestWorker(), daemon.TestProcessPidFile, logOpts)

	if err := proc.Start(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
