package running

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/scope"
)

// versionApp is the code of a version of the application: it writes the
// version into the data directory of the instance, so that the test sees
// which version runs and where the data goes.
const versionApp = `local fio = require('fio')
local path = fio.pathjoin(box.cfg.wal_dir, 'version')
local file = fio.open(path, {'O_CREAT', 'O_WRONLY', 'O_TRUNC'}, tonumber('644', 8))
file:write('%s')
file:close()
`

// socketFromRoot is the path of the console socket of storage-1 of myapp
// under a scope root.
const socketFromRoot = "/var/run/myapp/storage-1/tarantool.control"

// versionFiles returns the files of version of the application: one
// instance, storage-1, running app.lua of the version from its cluster
// config.
func versionFiles(version string) map[string]string {
	files := appFiles("{restart: on-failure}", "app:\n  file: app.lua\n")

	files["app.lua"] = strings.Replace(versionApp, "%s", version, 1)

	return files
}

// longDir returns a fresh directory deep enough that a path of extra more
// bytes under it does not fit a unix socket address.
func longDir(t *testing.T, extra int) string {
	t.Helper()

	base := t.TempDir()

	return filepath.Join(base, strings.Repeat("x", max(1, 120-len(base)-extra)))
}

// installTarantoolLink makes the tarantool on PATH the one installed in the
// user or system scope at root for the version the lock bundles.
func installTarantoolLink(t *testing.T, root string) {
	t.Helper()

	tarantoolPath, err := exec.LookPath("tarantool")
	require.NoError(t, err)

	installed := filepath.Join(root, "versions", "tarantool", "tarantool_"+fixtureTarantool,
		"tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(installed), 0o755))
	require.NoError(t, os.Symlink(tarantoolPath, installed))
}

// readPid reads the pid in the pid file at path, 0 while there is none.
func readPid(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}

	return pid
}

// startedInstance is an instance started by Start, the watchdog, in a
// goroutine of the test.
type startedInstance struct {
	t    *testing.T
	inst *InstanceCtx
	done chan error
	// pid is the pid of tarantool, once it has written its pid file.
	pid int
}

// startInstance runs Start for inst and waits for tarantool to come up: its
// pid file and its sockets exist, and its data directory holds the version
// file app.lua writes, with want in it.
func startInstance(t *testing.T, inst *InstanceCtx, want string) *startedInstance {
	t.Helper()

	started := &startedInstance{t: t, inst: inst, done: make(chan error, 1), pid: 0}

	go func() {
		started.done <- Start(&cmdcontext.CmdCtx{}, inst)
	}()

	t.Cleanup(func() {
		if started.pid != 0 {
			_ = syscall.Kill(started.pid, syscall.SIGKILL)
		}
	})

	tarantoolPid := filepath.Join(inst.RunDir, "tarantool.pid")

	running := assert.Eventually(t, func() bool {
		started.pid = readPid(tarantoolPid)

		_, consoleErr := os.Stat(inst.ConsoleSocket)
		_, binaryErr := os.Stat(inst.BinaryPort)

		return started.pid != 0 && consoleErr == nil && binaryErr == nil &&
			started.version() == want
	}, 20*time.Second, 50*time.Millisecond)
	if !running {
		log, _ := os.ReadFile(inst.Log)
		require.FailNow(t, "the instance did not start", "%s", log)
	}

	return started
}

// version returns what app.lua wrote into the data directory.
func (started *startedInstance) version() string {
	data, _ := os.ReadFile(filepath.Join(started.inst.WalDir, "version"))

	return string(data)
}

// stop stops the watchdog as tt stop does and waits for Start to return.
func (started *startedInstance) stop() {
	started.t.Helper()

	require.NoError(started.t, syscall.Kill(os.Getpid(), syscall.SIGINT))

	select {
	case err := <-started.done:
		require.NoError(started.t, err)
	case <-time.After(40 * time.Second):
		require.FailNow(started.t, "the watchdog did not end")
	}

	assert.NoFileExists(started.t, started.inst.PIDFile)
	assertNoSockets(started.t, started.inst)
}

// TestInstalledInstanceStartsFromItsVersion starts an instance of an
// application installed into the user scope through NewInstanceCtx and the
// watchdog Start runs: tarantool of the application runs in the version
// directory, keeps its data, logs and sockets under the user root and writes
// nothing into the version, and switching apps/<app> to another version
// while it runs does not affect it.
func TestInstalledInstanceStartsFromItsVersion(t *testing.T) {
	root, _ := setScopeRoots(t)
	installTarantoolLink(t, root)

	firstVersion, link := installVersion(t, root, "1.0.0", versionFiles("1"))
	firstEntries, err := os.ReadDir(firstVersion)
	require.NoError(t, err)

	fixture := appFixture{scope: app.ScopeUser, root: root, dir: firstVersion, link: link}
	inst := fixture.instanceCtx(t)
	started := startInstance(t, &inst, "1")

	data := filepath.Join(root, "var", "lib", fixtureApp, fixtureInst)
	assert.Equal(t, data, inst.WalDir)
	assert.Equal(t, os.Getpid(), readPidFile(t, inst.PIDFile), "the pid file names the watchdog")
	assert.FileExists(t, filepath.Join(root, "var", "log", fixtureApp, fixtureInst, "tt.log"))

	snapshots, err := filepath.Glob(filepath.Join(data, "*.snap"))
	require.NoError(t, err)
	assert.NotEmpty(t, snapshots, "the data goes under the user root")

	secondVersion, _ := installVersion(t, root, "2.0.0", versionFiles("2"))
	switched, err := app.Load(t.Context(), fixtureApp,
		app.Options{Scope: app.ScopeUser, Template: "", Start: ""})
	require.NoError(t, err)
	require.Equal(t, secondVersion, switched.Dir, "apps/myapp points at the new version")

	// The running instance is not affected: it still runs, in the
	// directory of its version.
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, syscall.Kill(started.pid, 0), "the instance stopped")
	assert.Equal(t, "1", started.version())

	if runtime.GOOS == "linux" {
		cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(started.pid), "cwd"))
		require.NoError(t, err)
		assert.Equal(t, physicalPath(firstVersion), cwd)
	}

	started.stop()

	entriesAfter, err := os.ReadDir(firstVersion)
	require.NoError(t, err)
	assert.Equal(t, firstEntries, entriesAfter, "the instance wrote into its version")
}

// TestInstalledInstanceLongSocketPath pins that an installed instance whose
// console socket does not fit a unix socket address is refused by tt, before
// anything runs or is created, rather than failing to bind in tarantool: the
// config of tarantool binds a console socket outside of its working
// directory by the full path.
func TestInstalledInstanceLongSocketPath(t *testing.T) {
	setScopeRoots(t)

	root := longDir(t, len(socketFromRoot))
	t.Setenv(scope.UserRootEnv, root)
	installTarantoolLink(t, root)

	dir, link := installVersion(t, root, "1.0.0", versionFiles("1"))
	fixture := appFixture{scope: app.ScopeUser, root: root, dir: dir, link: link}
	inst := fixture.instanceCtx(t)

	require.Error(t, verifySocketLength(inst.ConsoleSocket), "the root is not long enough")

	canStart, err := IsAbleToStartInstances([]InstanceCtx{inst}, &cmdcontext.CmdCtx{})
	assert.False(t, canStart)
	require.ErrorIs(t, err, errSocketPathIsLongerThanSymbols)

	err = Start(&cmdcontext.CmdCtx{}, &inst)
	require.ErrorIs(t, err, errSocketPathIsLongerThanSymbols)
	assert.NoDirExists(t, filepath.Join(root, "var"))
}

// TestProjectInstanceLongSocketPath starts an instance of a project deep
// enough that its sockets do not fit a unix socket address by the full
// path: they lie inside the working directory of tarantool, the project
// directory, and tarantool binds them by the relative path tt gives it.
func TestProjectInstanceLongSocketPath(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(longDir(t, len("/myapp/.tarantool"+socketFromRoot)), fixtureApp)
	writeFiles(t, dir, versionFiles("1"))

	fixture := appFixture{scope: app.ScopeProject, root: scope.ProjectRoot(dir), dir: dir, link: ""}
	inst := fixture.instanceCtx(t)

	require.Error(t, verifySocketLength(inst.ConsoleSocket), "the path is not long enough")

	started := startInstance(t, &inst, "1")

	assert.Equal(t, filepath.Join(dir, ".tarantool", "var", "lib", fixtureApp, fixtureInst),
		inst.WalDir)

	started.stop()
}
