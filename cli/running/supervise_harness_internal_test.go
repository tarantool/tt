package running

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/ttlog"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

var errTampered = errors.New("a checked file changed")

// tamperedRepository is an integrity repository whose files have changed.
type tamperedRepository struct {
	mockRepository
}

func (*tamperedRepository) ValidateAll() error {
	return errTampered
}

// watchdogTest is the watchdog of a script instance in a temporary directory,
// with a configuration the test decides.
type watchdogTest struct {
	t       *testing.T
	cmdCtx  *cmdcontext.CmdCtx
	inst    InstanceCtx
	flag    string
	log     *lockedBuffer
	src     *instanceSource
	engine  *supervisor.Engine
	mu      sync.Mutex
	started []int
	exits   []supervisor.Exit
	// restartable is what the configuration says, read at every exit.
	restartable bool
}

// testRestartDelay is the restart delay of the watchdog of a test.
const testRestartDelay = 100 * time.Millisecond

func newWatchdogTest(t *testing.T, restartable bool, cmdCtx *cmdcontext.CmdCtx,
	configure ...func(opts *supervisor.Options),
) *watchdogTest {
	t.Helper()

	appPath, err := filepath.Abs(filepath.Join(instTestAppDir, "dumb_test_app.lua"))
	require.NoError(t, err)

	tarantool, err := exec.LookPath("tarantool")
	require.NoError(t, err)

	cmdCtx.Cli.TarantoolCli.Executable = tarantool

	// The sockets go in a directory of their own, short enough for a unix
	// socket path.
	dir := t.TempDir()
	runDir := shortTempDir(t)
	test := &watchdogTest{
		t:      t,
		cmdCtx: cmdCtx,
		inst: InstanceCtx{
			AppDir:         dir,
			InstanceScript: appPath,
			WalDir:         dir,
			VinylDir:       dir,
			MemtxDir:       dir,
			PIDFile:        filepath.Join(dir, "tt.pid"),
			ConsoleSocket:  filepath.Join(runDir, "tarantool.control"),
			BinaryPort:     filepath.Join(runDir, "tarantool.sock"),
			Restartable:    restartable,
		},
		flag:        filepath.Join(dir, "started"),
		log:         &lockedBuffer{},
		restartable: restartable,
	}

	t.Setenv("started_flag_file", test.flag)

	test.src = newInstanceSource(cmdCtx, &test.inst, &watchdogLog{
		logger:      ttlog.NewCustomLogger(test.log, "Watchdog ", 0),
		checkPeriod: time.Duration(cmdCtx.Cli.IntegrityCheckPeriod) * time.Second,
	}, func() (InstanceCtx, error) {
		test.mu.Lock()
		defer test.mu.Unlock()

		inst := test.inst

		inst.Restartable = test.restartable

		return inst, nil
	})

	opts := watchdogOptions(cmdCtx, test.src)
	onEvent := opts.OnEvent

	opts.RestartDelay = testRestartDelay
	opts.OnEvent = func(event supervisor.Event) {
		switch event := event.(type) {
		case supervisor.Started:
			test.recordStart(event.Pid)
		case supervisor.Exit:
			test.recordExit(event)
		}

		onEvent(event)
	}

	for _, change := range configure {
		change(&opts)
	}

	test.engine, err = supervisor.New(test.src, opts)
	require.NoError(t, err)

	return test
}

// run runs the watchdog, logging its end as Start does, and returns the
// channel Run's result comes on.
func (test *watchdogTest) run() <-chan error {
	done := make(chan error, 1)

	go func() {
		err := test.engine.Run(context.Background())
		test.src.log.onEnd(err)

		done <- err
	}()

	test.t.Cleanup(func() {
		for _, pid := range test.children() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	return done
}

func (test *watchdogTest) recordStart(pid int) {
	test.mu.Lock()
	defer test.mu.Unlock()

	test.started = append(test.started, pid)
}

func (test *watchdogTest) recordExit(exit supervisor.Exit) {
	test.mu.Lock()
	defer test.mu.Unlock()

	test.exits = append(test.exits, exit)
}

func (test *watchdogTest) exitsSoFar() []supervisor.Exit {
	test.mu.Lock()
	defer test.mu.Unlock()

	return append([]supervisor.Exit(nil), test.exits...)
}

func (test *watchdogTest) children() []int {
	test.mu.Lock()
	defer test.mu.Unlock()

	return append([]int(nil), test.started...)
}

// waitStarted waits for the nth start of tarantool and returns its pid.
func (test *watchdogTest) waitStarted(nth int) int {
	test.t.Helper()

	require.Eventually(test.t, func() bool {
		_, err := os.Stat(test.flag)

		return err == nil && len(test.children()) >= nth
	}, 20*time.Second, 10*time.Millisecond, "tarantool did not start")

	require.NoError(test.t, os.Remove(test.flag))

	return test.children()[nth-1]
}

// waitDone waits for Run to return.
func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(40 * time.Second):
		require.FailNow(t, "the watchdog did not end")

		return nil
	}
}

// shortTempDir returns a directory under /tmp, whose path leaves room for a
// unix socket in it.
func shortTempDir(t *testing.T) string {
	t.Helper()

	//nolint:usetesting // t.TempDir is too deep for a socket path on macOS.
	dir, err := os.MkdirTemp("/tmp", "ttw")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

// moveRunDir changes where the configuration puts the sockets, as an edit of
// run_dir would, and returns the new context.
func (test *watchdogTest) moveRunDir() InstanceCtx {
	test.t.Helper()

	runDir := shortTempDir(test.t)

	test.mu.Lock()
	defer test.mu.Unlock()

	test.inst.ConsoleSocket = filepath.Join(runDir, "tarantool.control")
	test.inst.BinaryPort = filepath.Join(runDir, "tarantool.sock")

	return test.inst
}

// setRestartable changes what the configuration says of restart_on_failure.
func (test *watchdogTest) setRestartable(restartable bool) {
	test.mu.Lock()
	defer test.mu.Unlock()

	test.restartable = restartable
}

// waitSockets waits for the sockets of inst to exist.
func waitSockets(t *testing.T, inst *InstanceCtx) {
	t.Helper()

	require.Eventually(t, func() bool {
		_, consoleErr := os.Stat(inst.ConsoleSocket)
		_, binaryErr := os.Stat(inst.BinaryPort)

		return consoleErr == nil && binaryErr == nil
	}, 20*time.Second, 10*time.Millisecond, "tarantool did not create its sockets")
}

// assertNoSockets asserts that the sockets of inst are gone.
func assertNoSockets(t *testing.T, inst *InstanceCtx) {
	t.Helper()

	assert.NoFileExists(t, inst.ConsoleSocket)
	assert.NoFileExists(t, inst.BinaryPort)
}

// readPidFile reads the pid in a pid file.
func readPidFile(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err)

	return pid
}
