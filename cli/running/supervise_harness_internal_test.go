package running

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/ttlog"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/scope"
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

// watchdogApp is the code of the instance of a watchdog test: it marks its
// start in the file started_flag_file names, and exits at once with the
// status exit_status gives, when it gives one.
const watchdogApp = `local fio = require('fio')
fio.open(os.getenv('started_flag_file'), {'O_CREAT'}):close()

local status = os.getenv('exit_status')
if status ~= nil and status ~= '' then
    os.exit(tonumber(status))
end
`

// watchdogTest is the watchdog of an instance of a project in a temporary
// directory, with a restart policy the test decides.
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
	// restart is the restart policy of the instance record, read at every
	// exit.
	restart instances.RestartPolicy
}

// testRestartDelay is the restart delay of the watchdog of a test.
const testRestartDelay = 100 * time.Millisecond

func newWatchdogTest(t *testing.T, restart instances.RestartPolicy, cmdCtx *cmdcontext.CmdCtx,
	configure ...func(opts *supervisor.Options),
) *watchdogTest {
	t.Helper()

	setScopeRoots(t)

	// The project is short enough for the sockets of the instance under its
	// .tarantool, which tarantool binds by the full path then.
	dir := filepath.Join(shortTempDir(t), fixtureApp)
	writeFiles(t, dir, appFiles("", "app:\n  file: app.lua\n"))
	writeFiles(t, dir, map[string]string{"app.lua": watchdogApp})

	fixture := appFixture{scope: app.ScopeProject, root: scope.ProjectRoot(dir), dir: dir, link: ""}

	test := &watchdogTest{
		t:       t,
		cmdCtx:  cmdCtx,
		inst:    fixture.instanceCtx(t),
		flag:    filepath.Join(dir, "started"),
		log:     &lockedBuffer{},
		restart: restart,
	}

	require.NoError(t, createInstanceDataDirectories(test.inst))

	// The watchdog logs into test.log, a logger of no file: with a log file
	// in the context it would switch to that file at the first restart.
	test.inst.Log = ""

	t.Setenv("started_flag_file", test.flag)
	t.Setenv("exit_status", "")

	test.src = newInstanceSource(cmdCtx, &test.inst, &watchdogLog{
		logger:      ttlog.NewCustomLogger(test.log, "Watchdog ", 0),
		checkPeriod: time.Duration(cmdCtx.Cli.IntegrityCheckPeriod) * time.Second,
	}, func() (InstanceCtx, error) {
		test.mu.Lock()
		defer test.mu.Unlock()

		inst := test.inst

		inst.Restart = test.restart

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

	var err error

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

// setRestart changes the restart policy of the instance record.
func (test *watchdogTest) setRestart(policy instances.RestartPolicy) {
	test.mu.Lock()
	defer test.mu.Unlock()

	test.restart = policy
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
