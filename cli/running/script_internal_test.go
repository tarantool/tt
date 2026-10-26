package running

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
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
	"github.com/tarantool/tt/v3/internal/instances"
)

// scriptApp is the script of storage-1 in scriptFiles: it configures the
// instance with box.cfg, leaving the paths to tt, and writes its pid and a
// mark into its data directory, so that the test sees that it runs and where
// the data goes.
const scriptApp = `local fio = require('fio')
box.cfg{}
local function put(name, data)
    local path = fio.pathjoin(box.cfg.wal_dir, name)
    local file = fio.open(path, {'O_CREAT', 'O_WRONLY', 'O_TRUNC'}, tonumber('644', 8))
    file:write(data)
    file:close()
end
put('pid', tostring(box.info.pid))
put('started', 'yes')
`

// scriptFiles returns the files of an application whose default template
// runs storage-1 from its script, init.lua, with the fields record adds to
// the record, a YAML flow mapping without braces, or none. The pair has no
// config: a description of instances that run their scripts needs none.
func scriptFiles(record string) map[string]string {
	fields := "script: init.lua"
	if record != "" {
		fields += ", " + record
	}

	return map[string]string{
		"app.manifest.toml":                 fixtureManifest,
		"app.manifest.lock":                 fixtureLock,
		".templates/default/instances.yaml": fixtureInst + ": {" + fields + "}\n",
		"init.lua":                          scriptApp,
	}
}

// TestNewInstanceCtxScript pins the context of an instance that runs its
// script in every scope: the paths of the layout, the script in the
// application directory, and no cluster config.
func TestNewInstanceCtxScript(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, scriptFiles(""))
			inst := fixture.instanceCtx(t)

			data := filepath.Join(fixture.root, "var", "lib", fixtureApp, fixtureInst)
			logs := filepath.Join(fixture.root, "var", "log", fixtureApp, fixtureInst)
			run := filepath.Join(fixture.root, "var", "run", fixtureApp, fixtureInst)

			assert.True(t, IsScript(&inst))
			assert.False(t, IsExternal(&inst))
			assert.Equal(t, instances.KindScript, inst.Kind)
			assert.Equal(t, filepath.Join(fixture.dir, "init.lua"), inst.InstanceScript)
			assert.Equal(t, fixture.dir, inst.AppDir)
			assert.Equal(t, data, inst.WalDir)
			assert.Equal(t, data, inst.MemtxDir)
			assert.Equal(t, data, inst.VinylDir)
			assert.Empty(t, inst.DataDir)
			assert.Equal(t, filepath.Join(logs, "tt.log"), inst.Log)
			assert.Equal(t, filepath.Join(run, "tt.pid"), inst.PIDFile)
			assert.Empty(t, inst.ProcessPIDFile)
			assert.Equal(t, filepath.Join(run, "tarantool.control"), inst.ConsoleSocket)
			assert.Equal(t, filepath.Join(run, "tarantool.sock"), inst.BinaryPort)
			assert.Empty(t, inst.ClusterConfigPath)
			assert.Nil(t, inst.Configuration)
			require.NoError(t, RequireTarantool(&inst))
		})
	}
}

// TestNewInstanceCtxScriptRecordEnv pins that the TT_ variables of box.cfg in
// the env of the record move the data of the instance, as they do in
// box.cfg, relative ones resolved against the application directory, while
// the variables of the cluster config mean nothing to it.
func TestNewInstanceCtxScriptRecordEnv(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, scriptFiles(
		"env: {TT_WAL_DIR: wal, TT_MEMTX_DIR: /data/snap, TT_SNAPSHOT_DIR: /elsewhere}"))
	inst := fixture.instanceCtx(t)

	data := filepath.Join(fixture.root, "var", "lib", fixtureApp, fixtureInst)

	assert.Equal(t, filepath.Join(fixture.dir, "wal"), inst.WalDir)
	assert.Equal(t, "/data/snap", inst.MemtxDir)
	assert.Equal(t, data, inst.VinylDir)
	require.NoError(t, checkRelativePaths(&inst), "a project resolves them against itself")
}

// TestScriptRelativePaths pins that an installed instance that runs its
// script is refused a relative path in the TT_ variables of box.cfg in the
// env of its record, which would have it write into its version.
func TestScriptRelativePaths(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, scriptFiles(
		"env: {TT_WAL_DIR: wal, TT_PID_FILE: run/tarantool.pid, TT_VINYL_DIR: /data/vinyl}"))
	inst := fixture.instanceCtx(t)

	err := checkStart(&inst)
	require.ErrorIs(t, err, errRelativePath)
	require.ErrorContains(t, err, `TT_WAL_DIR "wal", TT_PID_FILE "run/tarantool.pid"`)
	assert.NotContains(t, err.Error(), "TT_VINYL_DIR")
}

// TestScriptMissing pins that the command that starts an instance whose
// script is missing reports it, rather than the watchdog in its log.
func TestScriptMissing(t *testing.T) {
	files := scriptFiles("")
	delete(files, "init.lua")

	fixture := newAppFixture(t, app.ScopeProject, files)
	inst := fixture.instanceCtx(t)

	canStart, err := IsAbleToStartInstances([]InstanceCtx{inst})
	assert.False(t, canStart)
	require.ErrorIs(t, err, errNoScript)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, os.Mkdir(inst.InstanceScript, 0o755))

	err = checkStart(&inst)
	require.ErrorIs(t, err, errNoScript)
	require.ErrorIs(t, err, errIsDirectory)
}

// TestAppScriptSpec pins the Spec of an instance that runs its script in
// every scope: tarantool of the application runs the launcher in the
// application directory, and box.cfg gets the paths of the layout through
// its TT_ variables.
func TestAppScriptSpec(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, scriptFiles("env: {RECORD: set}"))
			tarantoolPath := fixture.installTarantool(t, fixtureTarantool)
			inst := fixture.instanceCtx(t)

			spec, err := instanceSpec(&inst, specOptions{
				integrity: nil, stdout: io.Discard, stderr: io.Discard, warn: nil,
			})
			require.NoError(t, err)

			assert.Equal(t, tarantoolPath, spec.Path)
			assert.Equal(t, []string{"-"}, spec.Args)
			assert.Equal(t, instanceLauncher, spec.Stdin)
			assert.Equal(t, fixture.dir, spec.Dir)
			assert.Equal(t, syscall.SIGINT, spec.StopSignal)
			assert.True(t, spec.ForwardStopSignal)
			assert.True(t, spec.ForwardReloadSignal)
			assert.False(t, spec.ProcessGroup)

			data := filepath.Join(fixture.root, "var", "lib", fixtureApp, fixtureInst)
			run := filepath.Join(fixture.root, "var", "run", fixtureApp, fixtureInst)

			for name, want := range map[string]string{
				"PWD":                       fixture.dir,
				"TT_CLI":                    "true",
				"TT_CLI_INSTANCE":           filepath.Join(fixture.dir, "init.lua"),
				"TT_CLI_WORK_DIR":           fixture.dir,
				"TT_CLI_CONSOLE_SOCKET":     "unix/:./tarantool.control",
				"TT_CLI_CONSOLE_SOCKET_DIR": run,
				"TT_WAL_DIR":                data,
				"TT_MEMTX_DIR":              data,
				"TT_VINYL_DIR":              data,
				"TARANTOOL_WORKDIR":         data,
				"TARANTOOL_APP_NAME":        fixtureApp,
				"TARANTOOL_INSTANCE_NAME":   fixtureInst,
				"TARANTOOL_LOG":             "",
				"RECORD":                    "set",
			} {
				value, found := envOf(spec.Env, name)
				if assert.True(t, found, name) {
					assert.Equal(t, want, value, name)
				}
			}

			listen, _ := envOf(spec.Env, "TT_LISTEN")
			assert.Equal(t, physicalPath(filepath.Join(run, "tarantool.sock")),
				socketTarget(spec.Dir, strings.TrimPrefix(listen, "unix/:")))

			for _, name := range []string{"TARANTOOL_CFG", "TT_WAL_DIR_DEFAULT"} {
				_, found := envOf(spec.Env, name)
				assert.False(t, found, name)
			}
		})
	}
}

// TestAppScriptSpecRecordEnv pins what the env of a record changes: the TT_
// variables of box.cfg, which configure the instance, but not the variables
// tt and the launcher run it with.
func TestAppScriptSpecRecordEnv(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, scriptFiles(
		"env: {TT_WAL_DIR: /data/wal, TT_LISTEN: '3301', PWD: /elsewhere, "+
			"TT_CLI_INSTANCE: /elsewhere/init.lua, TARANTOOL_CFG: /etc/cartridge.yml}"))
	fixture.installTarantool(t, fixtureTarantool)

	inst := fixture.instanceCtx(t)
	require.Equal(t, "/data/wal", inst.WalDir)

	spec, err := appScriptSpec(&inst, specOptions{})
	require.NoError(t, err)

	for name, want := range map[string]string{
		"TT_WAL_DIR":        "/data/wal",
		"TARANTOOL_WORKDIR": "/data/wal",
		"TT_LISTEN":         "3301",
		"TARANTOOL_CFG":     "/etc/cartridge.yml",
		"PWD":               fixture.dir,
		"TT_CLI_INSTANCE":   filepath.Join(fixture.dir, "init.lua"),
	} {
		value, _ := envOf(spec.Env, name)
		assert.Equal(t, want, value, name)
	}
}

// TestAppScriptSpecListenOfEnvironment pins that TT_LISTEN of the
// environment of tt reaches box.cfg, as it did in tt2.
func TestAppScriptSpecListenOfEnvironment(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, scriptFiles(""))
	fixture.installTarantool(t, fixtureTarantool)

	inst := fixture.instanceCtx(t)

	t.Setenv("TT_LISTEN", "3302")

	spec, err := appScriptSpec(&inst, specOptions{})
	require.NoError(t, err)

	listen, _ := envOf(spec.Env, "TT_LISTEN")
	assert.Equal(t, "3302", listen)
}

// TestAppScriptSpecIntegrity pins that tarantool checks the integrity of the
// application with the hashes in the application directory.
func TestAppScriptSpecIntegrity(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, scriptFiles(""))
	fixture.installTarantool(t, fixtureTarantool)

	inst := fixture.instanceCtx(t)

	spec, err := appScriptSpec(&inst, specOptions{integrity: &integrity.IntegrityCtx{}})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"--integrity-check", filepath.Join(fixture.dir, integrity.HashesFileName), "-",
	}, spec.Args)
}

// TestReloadRefusesScriptKindChange pins that the watchdog of an instance
// that runs its script does not run it once its record configures it by the
// cluster config instead.
func TestReloadRefusesScriptKindChange(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	writeFiles(t, dir, pairFiles("storage-1:\n  script: init.lua\n", "storage-1"))

	insts, err := collect(t, app.Options{Start: dir})
	require.NoError(t, err)
	require.True(t, IsScript(&insts[0]))

	writeFiles(t, dir, pairFiles("storage-1:\n", "storage-1"))

	_, err = reload(t.Context(), &insts[0], integrity.IntegrityCtx{Repository: nil})
	require.ErrorIs(t, err, errKindChanged)
	assert.ErrorContains(t, err,
		"it was a Tarantool instance that runs its script and is a Tarantool instance now")
}

// TestRequireClusterRecord pins the check of the commands that act on the
// instances of the cluster config: an instance that runs its script and an
// external process are refused, an instance of the cluster config and a name
// the description does not declare pass.
func TestRequireClusterRecord(t *testing.T) {
	application := &app.App{
		Name: fixtureApp,
		Instances: []instances.Instance{
			{Name: "storage-1", Restart: instances.RestartNo},
			{Name: "legacy-1", Script: "/app/init.lua", Restart: instances.RestartNo},
			{Name: externalInst, Binary: "python3", Restart: instances.RestartNo},
		},
	}

	require.NoError(t, RequireClusterRecord(application, "storage-1"))
	require.NoError(t, RequireClusterRecord(application, "storage-2"))
	require.NoError(t, RequireClusterRecord(application, ""))

	err := RequireClusterRecord(application, "legacy-1")
	require.ErrorIs(t, err, ErrNotClusterInstance)
	require.EqualError(t, err, "myapp:legacy-1 runs its script (script: /app/init.lua), "+
		"and no cluster config configures it: not an instance of the cluster config")

	err = RequireClusterRecord(application, externalInst)
	require.ErrorIs(t, err, ErrNotTarantool)
}

// TestScriptInstanceStarts starts an instance that runs its script, of an
// application installed into the user scope without a cluster config,
// through NewInstanceCtx and the watchdog Start runs: tarantool runs the
// script in the version directory, box.cfg puts the data under the user
// root, the console socket answers, and nothing is written into the
// version.
func TestScriptInstanceStarts(t *testing.T) {
	root, _ := setScopeRoots(t)
	installTarantoolLink(t, root)

	version, link := installVersion(t, root, "1.0.0", scriptFiles(""))
	entries, err := os.ReadDir(version)
	require.NoError(t, err)

	fixture := appFixture{scope: app.ScopeUser, root: root, dir: version, link: link}
	inst := fixture.instanceCtx(t)

	done := make(chan error, 1)

	go func() {
		done <- Start(t.Context(), &cmdcontext.CmdCtx{}, &inst)
	}()

	data := filepath.Join(root, "var", "lib", fixtureApp, fixtureInst)
	pidFile := filepath.Join(data, "pid")

	var pid int

	t.Cleanup(func() {
		if pid != 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	running := assert.Eventually(t, func() bool {
		pid = readPid(pidFile)

		_, consoleErr := os.Stat(inst.ConsoleSocket)
		_, binaryErr := os.Stat(inst.BinaryPort)
		_, startedErr := os.Stat(filepath.Join(data, "started"))

		return pid != 0 && consoleErr == nil && binaryErr == nil && startedErr == nil
	}, 20*time.Second, 50*time.Millisecond)
	if !running {
		log, _ := os.ReadFile(inst.Log)
		require.FailNow(t, "the instance did not start", "%s", log)
	}

	snapshots, err := filepath.Glob(filepath.Join(data, "*.snap"))
	require.NoError(t, err)
	assert.NotEmpty(t, snapshots, "the data goes under the user root")

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", inst.ConsoleSocket)
	require.NoError(t, err, "the console socket answers")

	_ = conn.Close()

	if runtime.GOOS == "linux" {
		cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
		require.NoError(t, err)
		assert.Equal(t, physicalPath(version), cwd)
	}

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(40 * time.Second):
		require.FailNow(t, "the watchdog did not end")
	}

	assert.NoFileExists(t, inst.PIDFile)
	assertNoSockets(t, &inst)

	after, err := os.ReadDir(version)
	require.NoError(t, err)
	assert.Equal(t, entries, after, "the instance wrote into its version")
}
