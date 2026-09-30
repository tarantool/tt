package running

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

const filePollInterval = 500 * time.Millisecond

// waitForFile waits for the file to appear.
func waitForFile(filePath string) int {
	retries := 10
	for retries > 0 {
		time.Sleep(filePollInterval)

		_, err := os.Stat(filePath)
		if err == nil {
			break
		}

		retries--
	}

	return retries
}

// lockedBuffer is a buffer written by the watchdog and the output of
// tarantool at once, and read by the test meanwhile.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (lb *lockedBuffer) Write(data []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	return lb.buf.Write(data)
}

// Read reads what has been written and not read yet; io.EOF means nothing
// has, for now.
func (lb *lockedBuffer) Read(data []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	return lb.buf.Read(data)
}

func (lb *lockedBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	return lb.buf.String()
}

// specRun is a Spec of an instance running as a process of the test.
type specRun struct {
	cmd     *exec.Cmd
	spec    supervisor.Spec
	done    chan struct{}
	waitErr error
}

// startSpec runs spec once, as tt start --interactive does.
func startSpec(t *testing.T, ctx context.Context, spec supervisor.Spec) *specRun {
	t.Helper()

	run := &specRun{cmd: spec.Command(ctx), spec: spec, done: make(chan struct{}), waitErr: nil}
	require.NoError(t, run.cmd.Start())

	go func() {
		run.waitErr = run.cmd.Wait()

		close(run.done)
	}()

	return run
}

// alive reports whether the process has not exited.
func (run *specRun) alive() bool {
	select {
	case <-run.done:
		return false
	default:
		return true
	}
}

// wait waits for the process to exit and returns what exec.Cmd.Wait did.
func (run *specRun) wait() error {
	<-run.done

	return run.waitErr
}

// stop stops the process with the stop signal of its Spec, and kills it if
// it has not exited by stopTimeout.
func (run *specRun) stop() {
	if !run.alive() {
		return
	}

	_ = run.cmd.Process.Signal(run.spec.StopSignal)

	select {
	case <-run.done:
	case <-time.After(stopTimeout):
		_ = run.cmd.Process.Kill()

		<-run.done
	}
}
