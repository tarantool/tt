package running

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
)

// externalInst is the name of the external process of the fixtures.
const externalInst = "worker-1"

// externalFiles returns the files of an application whose default template
// runs storage-1, a Tarantool instance, and worker-1, an external process
// with the fields record, a YAML flow mapping.
func externalFiles(record string) map[string]string {
	return pairFiles("storage-1:\n"+externalInst+": "+record+"\n", "storage-1")
}

// instanceCtxOf loads the application of the fixture and builds the context
// of its instance name.
func (fixture appFixture) instanceCtxOf(t *testing.T, name string) InstanceCtx {
	t.Helper()

	loaded := fixture.load(t)

	for _, record := range loaded.Instances {
		if record.Name == name {
			inst, err := NewInstanceCtx(loaded, record)
			require.NoError(t, err)

			return inst
		}
	}

	require.FailNow(t, "no such instance", name)

	return InstanceCtx{}
}

// writeExecutable writes a shell script that exits at once at path.
func writeExecutable(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

// TestNewInstanceCtxExternal pins the context of an external process in
// every scope: its binary from the application directory, its log and pid
// files in the layout of the scope, its data directory in the layout as its
// working directory without workdir, and no sockets or config.
func TestNewInstanceCtxExternal(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, externalFiles(
				`{binary: ./bin/worker, args: [-v, "1"], env: {FOO: bar}, `+
					`stop_signal: SIGUSR1, stop_timeout: 3s, logrotate_signal: HUP, `+
					`restart: on-failure}`))
			loaded := fixture.load(t)

			insts, err := InstancesOf(loaded, loaded.Instances)
			require.NoError(t, err)
			require.Len(t, insts, 2)

			tarantool, inst := insts[0], insts[1]
			assert.False(t, IsExternal(&tarantool))
			assert.Equal(t, instances.KindCluster, tarantool.Kind)
			assert.Empty(t, tarantool.ProcessPIDFile)
			require.NoError(t, RequireTarantool(&tarantool))

			assert.True(t, IsExternal(&inst))
			assert.Equal(t, instances.KindExternal, inst.Kind)
			assert.Equal(t, filepath.Join(loaded.Dir, "bin", "worker"), inst.Binary)
			assert.Equal(t, []string{"-v", "1"}, inst.Args)

			data := filepath.Join(fixture.root, "var", "lib", fixtureApp, externalInst)
			assert.Equal(t, data, inst.DataDir)
			assert.Equal(t, data, inst.Workdir)
			assert.Equal(t, loaded.Dir, inst.AppDir)
			assert.Equal(t, syscall.SIGHUP, inst.LogrotateSignal)
			assert.Equal(t, map[string]string{"FOO": "bar"}, inst.Env)
			assert.Equal(t, syscall.SIGUSR1, inst.StopSignal)
			assert.Equal(t, 3*time.Second, inst.StopTimeout)
			assert.Equal(t, instances.RestartOnFailure, inst.Restart)

			logs := filepath.Join(fixture.root, "var", "log", fixtureApp, externalInst)
			run := filepath.Join(fixture.root, "var", "run", fixtureApp, externalInst)

			assert.Equal(t, logs, inst.LogDir)
			assert.Equal(t, filepath.Join(logs, "tt.log"), inst.Log)
			assert.Equal(t, run, inst.RunDir)
			assert.Equal(t, filepath.Join(run, "tt.pid"), inst.PIDFile)
			assert.Equal(t, filepath.Join(run, "process.pid"), inst.ProcessPIDFile)

			for name, value := range map[string]string{
				"WalDir": inst.WalDir, "MemtxDir": inst.MemtxDir, "VinylDir": inst.VinylDir,
				"ConsoleSocket": inst.ConsoleSocket, "BinaryPort": inst.BinaryPort,
				"ClusterConfigPath": inst.ClusterConfigPath,
			} {
				assert.Empty(t, value, name)
			}

			assert.Nil(t, inst.Configuration)

			err = RequireTarantool(&inst)
			require.ErrorIs(t, err, ErrNotTarantool)
			assert.ErrorContains(t, err, "myapp:worker-1 is an external process")
		})
	}
}

// TestNewInstanceCtxExternalHostFiles pins that the relative binary and
// workdir of the pair of the host, <root>/etc/<app>/, mean paths in the
// application directory, the version, as they do in its templates.
func TestNewInstanceCtxExternalHostFiles(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, pairFiles("storage-1:\n", "storage-1"))
	host := filepath.Join(fixture.root, "etc", fixtureApp)

	writeFiles(t, host, map[string]string{
		"instances.yaml": externalInst + ": {binary: bin/worker, workdir: ./work}\n",
		"config.yaml":    clusterConfig(""),
	})

	loaded := fixture.load(t)
	require.True(t, loaded.Pair.Host())

	inst := fixture.instanceCtxOf(t, externalInst)
	assert.Equal(t, filepath.Join(loaded.Dir, "bin", "worker"), inst.Binary)
	assert.Equal(t, filepath.Join(loaded.Dir, "work"), inst.Workdir)
	assert.Empty(t, inst.DataDir, "a process with workdir has no data directory")
	assert.NotContains(t, inst.Binary, host)
}

// TestExternalSpec pins the Spec of an external process: its binary with the
// arguments of its record in its working directory, the env of the record
// on top of the inherited environment and nothing of tarantool, SIGTERM and
// the stop timeout of a Tarantool instance unless the record says otherwise,
// no forwarding of the stop signal, and a process group of its own.
func TestExternalSpec(t *testing.T) {
	setScopeRoots(t)
	t.Setenv("RUNNING_TEST_INHERITED", "yes")

	fixture := newAppFixture(t, app.ScopeProject, externalFiles(
		`{binary: ./bin/worker, args: [-v, "x y"], workdir: ./work, env: {FOO: bar}}`))
	writeExecutable(t, filepath.Join(fixture.dir, "bin", "worker"))
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "work"), 0o755))

	inst := fixture.instanceCtxOf(t, externalInst)
	buf := &lockedBuffer{}

	spec, err := instanceSpec(&inst, specOptions{stdout: buf, stderr: buf})
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(fixture.dir, "bin", "worker"), spec.Path)
	assert.Equal(t, []string{"-v", "x y"}, spec.Args)
	assert.Equal(t, filepath.Join(fixture.dir, "work"), spec.Dir)
	assert.Nil(t, spec.Stdin)
	assert.Same(t, buf, spec.Stdout)
	assert.Same(t, buf, spec.Stderr)
	assert.Equal(t, syscall.SIGTERM, spec.StopSignal)
	assert.False(t, spec.ForwardStopSignal)
	assert.Equal(t, instanceStopTimeout, spec.StopTimeout)
	assert.True(t, spec.ProcessGroup)
	assert.Equal(t, syscall.Signal(0), spec.ReloadSignal, "no logrotate_signal, no signal")
	assert.False(t, spec.ForwardReloadSignal)
	assert.NoDirExists(t, filepath.Join(fixture.root, "var", "lib"))

	for name, want := range map[string]string{
		"FOO":                    "bar",
		"PWD":                    filepath.Join(fixture.dir, "work"),
		"RUNNING_TEST_INHERITED": "yes",
	} {
		value, found := envOf(spec.Env, name)
		assert.True(t, found, name)
		assert.Equal(t, want, value, name)
	}

	for _, name := range []string{
		"TT_LOG_FILE_DEFAULT", "TT_WAL_DIR_DEFAULT", "TT_CONSOLE_SOCKET_DEFAULT",
		"TT_IPROTO_LISTEN_DEFAULT", "TT_PROCESS_PID_FILE_DEFAULT",
	} {
		_, found := envOf(spec.Env, name)
		assert.False(t, found, name)
	}
}

// TestExternalSpecRecordSettings pins that the stop settings, the
// logrotate_signal and the PWD of the record win over the defaults, and that
// a process without workdir runs in its data directory, which the Spec
// creates.
func TestExternalSpecRecordSettings(t *testing.T) {
	setScopeRoots(t)

	fixture := newAppFixture(t, app.ScopeProject, externalFiles(
		`{binary: ./bin/worker, stop_signal: INT, stop_timeout: 2m, logrotate_signal: USR2, `+
			`env: {PWD: /elsewhere}}`))
	writeExecutable(t, filepath.Join(fixture.dir, "bin", "worker"))

	inst := fixture.instanceCtxOf(t, externalInst)
	data := filepath.Join(fixture.dir, ".tarantool", "var", "lib", fixtureApp, externalInst)
	require.NoDirExists(t, data)

	spec, err := instanceSpec(&inst, specOptions{})
	require.NoError(t, err)

	assert.Equal(t, syscall.SIGINT, spec.StopSignal)
	assert.False(t, spec.ForwardStopSignal)
	assert.Equal(t, 2*time.Minute, spec.StopTimeout)
	assert.Equal(t, syscall.SIGUSR2, spec.ReloadSignal)
	assert.False(t, spec.ForwardReloadSignal)
	assert.Equal(t, data, spec.Dir)
	assert.DirExists(t, data)

	pwd, _ := envOf(spec.Env, "PWD")
	assert.Equal(t, "/elsewhere", pwd)
}

// TestExternalStartChecks pins what stops an external process from starting
// before anything runs or is created: a binary that is missing or not
// executable, a program name not in PATH, and a working directory that is
// not a directory. An external process needs no tarantool.
func TestExternalStartChecks(t *testing.T) {
	setScopeRoots(t)

	pathDir := t.TempDir()
	writeExecutable(t, filepath.Join(pathDir, "ttfixture-worker"))
	// No tarantool anywhere: an external process does not need one.
	t.Setenv("PATH", pathDir)

	cases := []struct {
		name    string
		record  string
		prepare func(t *testing.T, dir string)
		want    error
	}{
		{
			name:   "ok",
			record: `{binary: ./bin/worker}`,
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				writeExecutable(t, filepath.Join(dir, "bin", "worker"))
			},
		},
		{name: "in PATH", record: `{binary: ttfixture-worker}`},
		{name: "missing", record: `{binary: ./bin/worker}`, want: errNoExecutable},
		{name: "not in PATH", record: `{binary: ttfixture-missing}`, want: exec.ErrNotFound},
		{
			name:   "not executable",
			record: `{binary: ./bin/worker}`,
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				writeFiles(t, dir, map[string]string{"bin/worker": "#!/bin/sh\n"})
			},
			want: errNoExecutable,
		},
		{name: "no workdir", record: `{binary: ttfixture-worker, workdir: ./work}`,
			want: errNoWorkdir},
		{
			name:   "workdir is a file",
			record: `{binary: ttfixture-worker, workdir: ./work}`,
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				writeFiles(t, dir, map[string]string{"work": ""})
			},
			want: errNoWorkdir,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAppFixture(t, app.ScopeProject, externalFiles(test.record))
			if test.prepare != nil {
				test.prepare(t, fixture.dir)
			}

			inst := fixture.instanceCtxOf(t, externalInst)

			canStart, err := IsAbleToStartInstances([]InstanceCtx{inst})
			_, specErr := instanceSpec(&inst, specOptions{})

			if test.want == nil {
				require.NoError(t, err)
				assert.True(t, canStart)
				require.NoError(t, specErr)

				return
			}

			assert.False(t, canStart)
			require.ErrorIs(t, err, test.want)
			require.ErrorContains(t, err, `instance "worker-1" of "myapp"`)
			require.ErrorIs(t, specErr, test.want)

			err = superviseInstance(nil, &inst, func() (InstanceCtx, error) {
				return inst, nil
			})
			require.ErrorIs(t, err, test.want)
			assert.NoDirExists(t, fixture.root, "nothing is created")
		})
	}
}

// TestCollectExternal pins that the commands find an external process among
// the instances of an application, in the order of the description, while a
// record that runs its script is still refused.
func TestCollectExternal(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	writeFiles(t, dir, pairFiles(
		"worker-1:\n  binary: ./bin/worker\nstorage-1:\nlegacy-1:\n  script: init.lua\n",
		"storage-1"))

	for _, target := range []string{"myapp:worker-1", "myapp:worker-1,myapp:storage-1"} {
		insts, err := collect(t, app.Options{Start: dir}, target)
		require.NoError(t, err, target)
		assert.Equal(t, "myapp:worker-1", GetAppInstanceName(insts[0]), target)
		assert.True(t, IsExternal(&insts[0]), target)
	}

	_, err := collect(t, app.Options{Start: dir}, "myapp:*")
	require.ErrorIs(t, err, ErrUnsupportedKind)
}

// TestReloadRefusesAnotherKind pins that the watchdog does not run an
// instance whose record has turned into another kind of instance since it
// started: the watchdog is set up for the kind it started.
func TestReloadRefusesAnotherKind(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	writeFiles(t, dir, pairFiles("storage-1:\n  binary: ./bin/worker\n", "storage-1"))

	insts, err := collect(t, app.Options{Start: dir})
	require.NoError(t, err)
	require.True(t, IsExternal(&insts[0]))

	reloaded, err := reload(t.Context(), &insts[0], integrity.IntegrityCtx{Repository: nil})
	require.NoError(t, err)
	assert.True(t, IsExternal(&reloaded))

	writeFiles(t, dir, pairFiles("storage-1:\n", "storage-1"))

	_, err = reload(t.Context(), &insts[0], integrity.IntegrityCtx{Repository: nil})
	require.ErrorIs(t, err, errNotReloaded)
	require.ErrorIs(t, err, errKindChanged)
	assert.ErrorContains(t, err, "it was an external process and is a Tarantool instance now")
}

// TestRequireTarantoolRecord pins the check of the commands that take an
// instance by its name without a context: an external process is refused,
// a Tarantool instance and a name the description does not declare pass.
func TestRequireTarantoolRecord(t *testing.T) {
	application := &app.App{
		Name: fixtureApp,
		Instances: []instances.Instance{
			{Name: "storage-1", Restart: instances.RestartNo},
			{Name: externalInst, Binary: "python3", Restart: instances.RestartNo},
		},
	}

	require.NoError(t, RequireTarantoolRecord(application, "storage-1"))
	require.NoError(t, RequireTarantoolRecord(application, "storage-2"))
	require.NoError(t, RequireTarantoolRecord(application, ""))

	err := RequireTarantoolRecord(application, externalInst)
	require.ErrorIs(t, err, ErrNotTarantool)
	assert.EqualError(t, err,
		"myapp:worker-1 is an external process (binary: python3): not a Tarantool instance")
}

// TestQuitRefusesExternal pins that tt kill --dump does not signal an
// external process, which would stop without a dump.
func TestQuitRefusesExternal(t *testing.T) {
	run := killRun(t)

	run.Kind = instances.KindExternal
	run.AppName = fixtureApp
	run.InstName = externalInst
	run.Binary = "python3"

	watchdog := sleeper(t, true)
	pid := watchdog.Process.Pid

	require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(pid)), 0o600))

	err := Quit(*run)
	require.ErrorIs(t, err, ErrNotTarantool)
	require.NoError(t, syscall.Kill(pid, 0), "the watchdog got a signal")
}
