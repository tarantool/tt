package running

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// The test binary doubles as the external process of the tests: with
// processModeEnv set in its environment, which the env of an instance record
// sets, TestMain runs the process instead of the tests.
const (
	processModeEnv = "RUNNING_TEST_PROCESS"
	// processDirEnv is the directory the process writes what it sees into.
	processDirEnv = "RUNNING_TEST_DIR"
	// processExitEnv is the exit status of processExit.
	processExitEnv = "RUNNING_TEST_EXIT"
	// inheritedEnv is a variable of the test, which the process inherits.
	inheritedEnv = "RUNNING_TEST_INHERITED"
)

// The modes of the process.
const (
	// processServe writes its run file, then records the signals it gets,
	// and exits on any of them but SIGHUP, which a log rotation sends.
	processServe = "serve"
	// processStubborn records the signals it gets and never exits on its
	// own.
	processStubborn = "stubborn"
	// processExit writes its run file and exits at once with the status
	// processExitEnv gives.
	processExit = "exit"
	// processWrapper starts a processServe child in its own process group,
	// writes the pid of the child into the child file, records the signals
	// it gets without passing them on, and exits once the child has exited,
	// as a shell script that runs a program does.
	processWrapper = "wrapper"
)

// processSignals are the signals the process records.
var processSignals = []os.Signal{
	syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT,
	syscall.SIGUSR1, syscall.SIGUSR2,
}

func TestMain(m *testing.M) {
	mode := os.Getenv(processModeEnv)
	if mode != "" {
		os.Exit(runProcess(mode))
	}

	os.Exit(m.Run())
}

// processRun is what the process writes about itself as it starts, into
// its run file, run.<pid> in the directory processDirEnv names.
type processRun struct {
	Pid  int               `json:"pid"`
	Pgid int               `json:"pgid"`
	Args []string          `json:"args"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
}

func runProcess(mode string) int {
	dir := os.Getenv(processDirEnv)

	switch mode {
	case processServe, processStubborn:
		return serveProcess(dir, mode == processStubborn)
	case processExit:
		writeProcessRun(dir)

		code, err := strconv.Atoi(os.Getenv(processExitEnv))
		if err != nil {
			panic(err)
		}

		return code
	case processWrapper:
		return wrapProcess(dir)
	}

	fmt.Fprintf(os.Stderr, "unknown process mode %q\n", mode)

	return 2
}

// writeWhole writes a file whole: a reader sees it either absent or
// complete.
func writeWhole(path, content string) {
	temp := path + ".tmp"

	err := os.WriteFile(temp, []byte(content), 0o600)
	if err != nil {
		panic(err)
	}

	err = os.Rename(temp, path)
	if err != nil {
		panic(err)
	}
}

// writeProcessRun writes the run file of the process and reports its start
// on its standard output.
func writeProcessRun(dir string) {
	pid := os.Getpid()

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		panic(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	env := map[string]string{}

	for _, name := range []string{"FOO", "PWD", inheritedEnv} {
		value, found := os.LookupEnv(name)
		if found {
			env[name] = value
		}
	}

	data, err := json.Marshal(processRun{
		Pid: pid, Pgid: pgid, Args: os.Args[1:], Cwd: cwd, Env: env,
	})
	if err != nil {
		panic(err)
	}

	writeWhole(filepath.Join(dir, "run."+strconv.Itoa(pid)), string(data))

	_, _ = fmt.Fprintf(os.Stdout, "process %d started\n", pid)
}

// recordSignal appends sig to the signals file of the process.
func recordSignal(dir string, sig os.Signal) {
	path := filepath.Join(dir, "signals."+strconv.Itoa(os.Getpid()))

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}

	_, err = fmt.Fprintln(file, sig.String())
	if err != nil {
		panic(err)
	}

	err = file.Close()
	if err != nil {
		panic(err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "process %d got %s\n", os.Getpid(), sig)
}

func serveProcess(dir string, stubborn bool) int {
	sigs := make(chan os.Signal, 16)
	signal.Notify(sigs, processSignals...)

	writeProcessRun(dir)

	for sig := range sigs {
		recordSignal(dir, sig)

		if !stubborn && sig != syscall.SIGHUP {
			return 0
		}
	}

	return 0
}

func wrapProcess(dir string) int {
	sigs := make(chan os.Signal, 16)
	signal.Notify(sigs, processSignals...)

	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}

	child := exec.CommandContext(context.Background(), exe)

	child.Env = append(os.Environ(), processModeEnv+"="+processServe)
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr

	err = child.Start()
	if err != nil {
		panic(err)
	}

	writeWhole(filepath.Join(dir, "child"), strconv.Itoa(child.Process.Pid))
	writeProcessRun(dir)

	done := make(chan struct{})

	go func() {
		_ = child.Wait()

		close(done)
	}()

	for {
		select {
		case sig := <-sigs:
			recordSignal(dir, sig)
		case <-done:
			return 0
		}
	}
}

// processEnv returns the entries of the env of a record, a YAML flow mapping
// body, that run the test binary as the process in mode, writing into dir.
func processEnv(mode, dir string) string {
	return fmt.Sprintf("%s: %s, %s: %q, GORACE: atexit_sleep_ms=0",
		processModeEnv, mode, processDirEnv, dir)
}

// linkTestBinary makes path a link to the test binary.
func linkTestBinary(t *testing.T, path string) {
	t.Helper()

	exe, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.Symlink(exe, path))
}

// processFixture lays out a project whose worker-1 runs the test binary at
// bin/worker, as the process in mode with the fields extra of its record, a
// YAML flow mapping body. It returns the fixture, the context of worker-1
// and the directory the process writes into.
func processFixture(t *testing.T, mode, extra string) (appFixture, InstanceCtx, string) {
	t.Helper()

	files := t.TempDir()
	record := "{binary: ./bin/worker, env: {" + processEnv(mode, files) + "}" + extra + "}"
	fixture := newAppFixture(t, app.ScopeProject, externalFiles(record))

	linkTestBinary(t, filepath.Join(fixture.dir, "bin", "worker"))

	return fixture, fixture.instanceCtxOf(t, externalInst), files
}

// waitProcess waits for the process of inst to start and returns what it
// wrote about itself: its pid is in the pid file its watchdog writes, and it
// has written its run file.
func waitProcess(t *testing.T, inst *InstanceCtx, files string) processRun {
	t.Helper()

	var run processRun

	require.Eventually(t, func() bool {
		pid := readPid(inst.ProcessPIDFile)
		if pid == 0 {
			return false
		}

		data, err := os.ReadFile(filepath.Join(files, "run."+strconv.Itoa(pid)))
		if err != nil {
			return false
		}

		require.NoError(t, json.Unmarshal(data, &run))

		return true
	}, 20*time.Second, 10*time.Millisecond, "the process did not start")

	return run
}

// processRuns returns the run files the processes have written into files.
func processRuns(t *testing.T, files string) []string {
	t.Helper()

	runs, err := filepath.Glob(filepath.Join(files, "run.*[0-9]"))
	require.NoError(t, err)

	return runs
}

// signalsOf returns the signals the process pid has recorded in files.
func signalsOf(files string, pid int) []string {
	data, err := os.ReadFile(filepath.Join(files, "signals."+strconv.Itoa(pid)))
	if err != nil {
		return nil
	}

	return strings.Fields(strings.ReplaceAll(string(data), " ", "_"))
}

// signalNames returns the names of sigs as signalsOf returns them.
func signalNames(sigs ...syscall.Signal) []string {
	names := make([]string, 0, len(sigs))
	for _, sig := range sigs {
		names = append(names, strings.ReplaceAll(sig.String(), " ", "_"))
	}

	return names
}

// gone reports whether no process pid exists any more.
func gone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// runningWatchdog is the watchdog of an external process, run by Start in a
// goroutine of the test, as tt instance start --watchdog runs it.
type runningWatchdog struct {
	t    *testing.T
	inst *InstanceCtx
	done chan error
}

// startWatchdog runs Start for inst.
func startWatchdog(t *testing.T, inst *InstanceCtx) *runningWatchdog {
	t.Helper()

	watchdog := &runningWatchdog{t: t, inst: inst, done: make(chan error, 1)}

	go func() {
		watchdog.done <- Start(t.Context(), &cmdcontext.CmdCtx{}, inst)
	}()

	t.Cleanup(func() {
		pid := readPid(inst.ProcessPIDFile)
		if pid != 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})

	return watchdog
}

// stop stops the watchdog as tt instance stop does, with SIGINT, and waits
// for Start to return.
func (watchdog *runningWatchdog) stop() {
	watchdog.t.Helper()

	require.NoError(watchdog.t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	require.NoError(watchdog.t, waitDone(watchdog.t, watchdog.done))
}

// log returns the log of the watchdog, tt.log of the instance.
func (watchdog *runningWatchdog) log() string {
	data, _ := os.ReadFile(watchdog.inst.Log)

	return string(data)
}

// TestExternalProcessStartStop starts an external process as
// tt instance start does and stops it as tt instance stop does: it runs the
// binary with the arguments, the env and the working directory of its record
// in a process group of its own, its output goes to tt.log, its watchdog
// writes its pid file, nothing is created for data, and it gets its stop
// signal rather than the SIGINT that stopped the watchdog.
func TestExternalProcessStartStop(t *testing.T) {
	cases := []struct {
		name   string
		record string
		want   syscall.Signal
	}{
		{name: "default", record: "", want: syscall.SIGTERM},
		{name: "stop_signal", record: ", stop_signal: SIGUSR1", want: syscall.SIGUSR1},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			setScopeRoots(t)
			t.Setenv(inheritedEnv, "yes")

			fixture, inst, files := processFixture(t, processServe,
				`, args: [-v, "x y"], workdir: ./work`+test.record)
			require.NoError(t, os.Mkdir(filepath.Join(fixture.dir, "work"), 0o755))

			watchdog := startWatchdog(t, &inst)
			run := waitProcess(t, &inst, files)

			assert.Equal(t, []string{"-v", "x y"}, run.Args)
			assert.Equal(t, physicalPath(filepath.Join(fixture.dir, "work")), run.Cwd)
			assert.Equal(t, map[string]string{
				"PWD": filepath.Join(fixture.dir, "work"), inheritedEnv: "yes",
			}, run.Env)
			assert.Equal(t, run.Pid, run.Pgid, "the process leads a group of its own")
			assert.Equal(t, os.Getpid(), readPidFile(t, inst.PIDFile),
				"tt.pid names the watchdog")
			assert.Equal(t, process_utils.ProcStateRunning.Code, Status(&inst).Code)

			require.Eventually(t, func() bool {
				return strings.Contains(watchdog.log(), fmt.Sprintf("process %d started", run.Pid))
			}, 20*time.Second, 10*time.Millisecond, "the output does not reach tt.log")

			watchdog.stop()

			assert.Equal(t, signalNames(test.want), signalsOf(files, run.Pid))
			assert.True(t, gone(run.Pid))
			assert.NoFileExists(t, inst.PIDFile)
			assert.NoFileExists(t, inst.ProcessPIDFile)
			assert.NoDirExists(t, filepath.Join(fixture.root, "var", "lib"))
			assert.NotContains(t, watchdog.log(), "killed")
		})
	}
}

// externalWatchdog is the watchdog of an external process run in a
// goroutine of the test as Start runs it, reading the instance anew before
// every start, with a short restart delay, and stopped by its context.
type externalWatchdog struct {
	t      *testing.T
	inst   *InstanceCtx
	done   chan error
	cancel context.CancelFunc
}

func superviseExternal(t *testing.T, inst *InstanceCtx) *externalWatchdog {
	t.Helper()

	require.NoError(t, createInstanceDataDirectories(*inst))

	logger, err := createLogger(inst)
	require.NoError(t, err)

	cmdCtx := &cmdcontext.CmdCtx{}
	pinned := *inst
	src := newInstanceSource(cmdCtx, inst, &watchdogLog{logger: logger, checkPeriod: 0},
		func() (InstanceCtx, error) {
			return reload(t.Context(), &pinned, integrity.IntegrityCtx{Repository: nil})
		})

	opts := watchdogOptions(cmdCtx, src)

	opts.RestartDelay = testRestartDelay

	engine, err := supervisor.New(src, opts)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	watchdog := &externalWatchdog{t: t, inst: inst, done: make(chan error, 1), cancel: cancel}

	go func() {
		err := engine.Run(ctx)
		src.log.onEnd(err)

		watchdog.done <- err
	}()

	t.Cleanup(func() {
		cancel()

		pid := readPid(inst.ProcessPIDFile)
		if pid != 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})

	return watchdog
}

// stop stops the watchdog and waits for it to end.
func (watchdog *externalWatchdog) stop() error {
	watchdog.t.Helper()

	watchdog.cancel()

	return waitDone(watchdog.t, watchdog.done)
}

// log returns the log of the watchdog, tt.log of the instance.
func (watchdog *externalWatchdog) log() string {
	data, _ := os.ReadFile(watchdog.inst.Log)

	return string(data)
}

// TestExternalProcessStopTimeout pins that an external process that does not
// stop on its stop signal is killed, with its whole group, once its
// stop_timeout is over, rather than after the stop timeout of a Tarantool
// instance.
func TestExternalProcessStopTimeout(t *testing.T) {
	setScopeRoots(t)

	_, inst, files := processFixture(t, processStubborn,
		", stop_timeout: 1s")

	watchdog := superviseExternal(t, &inst)
	run := waitProcess(t, &inst, files)

	stopped := time.Now()

	require.NoError(t, watchdog.stop())

	elapsed := time.Since(stopped)
	assert.GreaterOrEqual(t, elapsed, time.Second)
	assert.Less(t, elapsed, instanceStopTimeout)
	assert.Equal(t, signalNames(syscall.SIGTERM), signalsOf(files, run.Pid))
	assert.True(t, gone(run.Pid))
	assert.Contains(t, watchdog.log(), "the instance was killed")
	assert.NoFileExists(t, inst.ProcessPIDFile)
}

// TestExternalProcessRestart pins that the watchdog of an external process
// follows its restart policy as it does for a Tarantool instance: on-failure
// restarts a process that exits with a non-zero status and only such one,
// and no never restarts it.
func TestExternalProcessRestart(t *testing.T) {
	cases := []struct {
		policy   string
		status   string
		restarts bool
	}{
		{policy: "on-failure", status: "3", restarts: true},
		{policy: "on-failure", status: "0", restarts: false},
		{policy: "no", status: "3", restarts: false},
	}

	for _, test := range cases {
		t.Run(test.policy+"/"+test.status, func(t *testing.T) {
			setScopeRoots(t)

			files := t.TempDir()
			record := fmt.Sprintf("{binary: ./bin/worker, restart: %s, env: {%s, %s: %q}}",
				test.policy, processEnv(processExit, files), processExitEnv, test.status)
			fixture := newAppFixture(t, app.ScopeProject, externalFiles(record))
			linkTestBinary(t, filepath.Join(fixture.dir, "bin", "worker"))

			inst := fixture.instanceCtxOf(t, externalInst)
			watchdog := superviseExternal(t, &inst)

			if test.restarts {
				require.Eventually(t, func() bool {
					return len(processRuns(t, files)) >= 2
				}, 20*time.Second, 10*time.Millisecond, "the process was not restarted")
				require.NoError(t, watchdog.stop())

				return
			}

			require.NoError(t, waitDone(t, watchdog.done), "the watchdog did not end")
			assert.Len(t, processRuns(t, files), 1)
			assert.Contains(t, watchdog.log(), "the Instance has shutdown")
			assert.NoFileExists(t, inst.PIDFile)
			assert.NoFileExists(t, inst.ProcessPIDFile)
		})
	}
}

// TestExternalProcessHostFiles starts an external process of an installed
// application described by the pair of the host, <root>/etc/<app>/: its
// relative binary and workdir are paths in the version it runs from.
func TestExternalProcessHostFiles(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, pairFiles("storage-1:\n", "storage-1"))
	files := t.TempDir()

	writeFiles(t, filepath.Join(fixture.root, "etc", fixtureApp), map[string]string{
		"instances.yaml": fmt.Sprintf("%s: {binary: bin/worker, workdir: ./work, env: {%s}}\n",
			externalInst, processEnv(processServe, files)),
		"config.yaml": clusterConfig(""),
	})
	linkTestBinary(t, filepath.Join(fixture.dir, "bin", "worker"))
	require.NoError(t, os.Mkdir(filepath.Join(fixture.dir, "work"), 0o755))

	inst := fixture.instanceCtxOf(t, externalInst)
	watchdog := superviseExternal(t, &inst)
	run := waitProcess(t, &inst, files)

	assert.Equal(t, physicalPath(filepath.Join(fixture.dir, "work")), run.Cwd)
	assert.Equal(t, filepath.Join(fixture.root, "var", "run", fixtureApp, externalInst,
		"process.pid"), inst.ProcessPIDFile)

	require.NoError(t, watchdog.stop())
	assert.True(t, gone(run.Pid))
}

// TestExternalProcessWrapper pins that the stop signal reaches the whole
// process group of an external process: a wrapper that does not pass it on
// stops once the program it runs has stopped on it, with no kill.
func TestExternalProcessWrapper(t *testing.T) {
	setScopeRoots(t)

	_, inst, files := processFixture(t, processWrapper, "")

	watchdog := startWatchdog(t, &inst)
	run := waitProcess(t, &inst, files)

	child, err := strconv.Atoi(waitFileContent(t, filepath.Join(files, "child")))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(files, "run."+strconv.Itoa(child)))

		return err == nil
	}, 20*time.Second, 10*time.Millisecond, "the child did not start")

	pgid, err := syscall.Getpgid(child)
	require.NoError(t, err)
	assert.Equal(t, run.Pid, pgid, "the child is in the group of the wrapper")

	watchdog.stop()

	assert.Equal(t, signalNames(syscall.SIGTERM), signalsOf(files, child))
	assert.Equal(t, signalNames(syscall.SIGTERM), signalsOf(files, run.Pid))
	assert.True(t, gone(child))
	assert.True(t, gone(run.Pid))
	assert.NotContains(t, watchdog.log(), "killed")
}

// waitFileContent waits for the file at path and returns what it holds. The
// file is taken as soon as it exists, so its writer puts it in place whole:
// with writeWhole, or, in a shell, by writing a temporary file and renaming it.
func waitFileContent(t *testing.T, path string) string {
	t.Helper()

	var content []byte

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)

		content = data

		return err == nil
	}, 20*time.Second, 10*time.Millisecond, "%s did not appear", path)

	return string(content)
}

// TestExternalProcessFromPath pins that a binary named without a slash is a
// program looked up in PATH of tt, and that a program PATH does not have is
// reported at the start, before anything is created.
func TestExternalProcessFromPath(t *testing.T) {
	setScopeRoots(t)

	pathDir := t.TempDir()
	linkTestBinary(t, filepath.Join(pathDir, "ttfixture-worker"))
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	files := t.TempDir()
	fixture := newAppFixture(t, app.ScopeProject, pairFiles(fmt.Sprintf(
		"%s: {binary: ttfixture-worker, env: {%s}}\nworker-2: {binary: ttfixture-missing}\n",
		externalInst, processEnv(processServe, files)), "storage-1"))

	inst := fixture.instanceCtxOf(t, externalInst)
	assert.Equal(t, "ttfixture-worker", inst.Binary)

	watchdog := startWatchdog(t, &inst)
	run := waitProcess(t, &inst, files)

	// Without workdir it runs in its data directory, which the start
	// creates.
	data := filepath.Join(fixture.dir, ".tarantool", "var", "lib", fixtureApp, externalInst)
	assert.Equal(t, data, inst.Workdir)
	assert.Equal(t, physicalPath(data), run.Cwd)
	assert.Equal(t, data, run.Env["PWD"])

	watchdog.stop()

	missing := fixture.instanceCtxOf(t, "worker-2")

	canStart, err := IsAbleToStartInstances([]InstanceCtx{inst, missing})
	assert.False(t, canStart)
	require.ErrorIs(t, err, exec.ErrNotFound)
	require.ErrorContains(t, err, `instance "worker-2" of "myapp": cannot run the binary`)
	require.ErrorContains(t, err, "ttfixture-missing")

	require.NoError(t, os.RemoveAll(fixture.root))

	err = Start(t.Context(), &cmdcontext.CmdCtx{}, &missing)
	require.ErrorIs(t, err, exec.ErrNotFound)
	assert.NoDirExists(t, fixture.root)
}

// TestExternalProcessLogrotate pins that tt instance logrotate reopens tt.log
// of an external process, where its output goes, and sends the process its
// logrotate_signal, the SIGHUP the watchdog got being no signal of the
// process; without logrotate_signal the process gets nothing and goes on.
func TestExternalProcessLogrotate(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		record string
		want   []string
	}{
		{name: "no logrotate_signal", mode: processServe, record: "", want: nil},
		{
			name: "logrotate_signal", mode: processStubborn,
			record: ", logrotate_signal: SIGUSR2, stop_timeout: 1s",
			want:   signalNames(syscall.SIGUSR2),
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			setScopeRoots(t)

			_, inst, files := processFixture(t, test.mode, test.record)

			watchdog := startWatchdog(t, &inst)
			run := waitProcess(t, &inst, files)

			rotated := inst.Log + ".1"
			require.NoError(t, os.Rename(inst.Log, rotated))
			require.NoError(t, Logrotate(&inst))

			require.Eventually(t, func() bool {
				return strings.Contains(watchdog.log(), "log file has been reopened")
			}, 20*time.Second, 10*time.Millisecond, "tt.log was not reopened")

			if test.want != nil {
				require.Eventually(t, func() bool {
					return slices.Equal(test.want, signalsOf(files, run.Pid))
				}, 20*time.Second, 10*time.Millisecond, "the process got no signal")
				require.Eventually(t, func() bool {
					return strings.Contains(watchdog.log(), fmt.Sprintf("process %d got", run.Pid))
				}, 20*time.Second, 10*time.Millisecond, "the output does not reach the new tt.log")
			} else {
				// The watchdog signals the process right after it has
				// reopened the log, if it does at all.
				time.Sleep(300 * time.Millisecond)
				assert.Empty(t, signalsOf(files, run.Pid))
			}

			assert.False(t, gone(run.Pid), "the process stopped on the log rotation")

			watchdog.stop()
		})
	}
}

// TestKillExternalProcess pins that tt instance kill kills the process group
// of an external process along with the group of its watchdog: the process
// leads a group of its own, which killing the group of the watchdog leaves
// alone.
func TestKillExternalProcess(t *testing.T) {
	run := killRun(t)

	run.Kind = instances.KindExternal
	run.ProcessPIDFile = filepath.Join(filepath.Dir(run.PIDFile), "process.pid")

	watchdog := sleeper(t, true)

	grandchildFile := filepath.Join(t.TempDir(), "grandchild")
	process := exec.CommandContext(t.Context(), "sh", "-c",
		`sleep 60 & echo $! > "$0.tmp" && mv "$0.tmp" "$0"; wait`, grandchildFile)

	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, process.Start())

	grandchild, err := strconv.Atoi(strings.TrimSpace(waitFileContent(t, grandchildFile)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	require.NoError(t, os.WriteFile(run.PIDFile,
		[]byte(strconv.Itoa(watchdog.Process.Pid)), 0o600))
	require.NoError(t, os.WriteFile(run.ProcessPIDFile,
		[]byte(strconv.Itoa(process.Process.Pid)), 0o600))

	require.NoError(t, Kill(*run))

	_, err = watchdog.Process.Wait()
	require.NoError(t, err)

	err = process.Wait()
	require.Error(t, err)
	assert.Equal(t, syscall.SIGKILL, termSignalOf(process.ProcessState))

	assert.Eventually(t, func() bool { return gone(grandchild) }, 20*time.Second,
		10*time.Millisecond, "the grandchild survived tt instance kill")
	assert.NoFileExists(t, run.PIDFile)
	assert.NoFileExists(t, run.ProcessPIDFile)
}

// termSignalOf is the signal that ended the process in state, or 0.
func termSignalOf(state *os.ProcessState) syscall.Signal {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0
	}

	return status.Signal()
}
