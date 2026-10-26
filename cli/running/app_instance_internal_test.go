package running

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/scope"
)

const (
	fixtureApp     = "myapp"
	fixtureInst    = "storage-1"
	fixtureVersion = "1.0.0"
	// fixtureTarantool is the tarantool the lock of the fixture bundles.
	fixtureTarantool = "3.8.1"

	fixtureManifest = "manifest_version = \"0.1\"\n\n[package]\nname = \"myapp\"\n\n" +
		"[platform]\ntarantool = \">=3.0.0\"\ntt = \">=3.0.0\"\n"
	fixtureLock = "lock_version = \"0.1\"\nmanifest_version = \"0.1\"\n" +
		"generated_by = \"tt 3.0.0\"\nmanifest_hash = \"sha256:0\"\n" +
		"bundled_tarantool_version = \"3.8.1\"\n"
)

var allScopes = []app.Scope{app.ScopeProject, app.ScopeUser, app.ScopeSystem}

// clusterConfig returns a cluster config declaring storage-1, with global,
// YAML of the global level, in front of the topology.
func clusterConfig(global string) string {
	return global + "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
		"          storage-1: {}\n"
}

// appFiles returns the files of an application whose default template runs
// storage-1 with the fields record, a YAML flow mapping or empty, and the
// cluster config with global in front of the topology.
func appFiles(record, global string) map[string]string {
	return map[string]string{
		"app.manifest.toml":                 fixtureManifest,
		"app.manifest.lock":                 fixtureLock,
		".templates/default/instances.yaml": fixtureInst + ": " + record + "\n",
		".templates/default/config.yaml":    clusterConfig(global),
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

// appFixture is an application laid out in a scope the way tt keeps it.
type appFixture struct {
	scope app.Scope
	// root is the root of the scope.
	root string
	// dir is the application directory: the project directory, or the
	// version directory.
	dir string
	// link is <root>/apps/<app> in the user and system scopes.
	link string
}

// setScopeRoots points the user and the system roots at fresh directories
// and returns them. They are short enough for the sockets of an instance
// under them, which tarantool binds by the full path there.
func setScopeRoots(t *testing.T) (string, string) {
	t.Helper()

	user := filepath.Join(shortTempDir(t), "user")
	system := filepath.Join(shortTempDir(t), "system")

	t.Setenv(scope.UserRootEnv, user)
	t.Setenv(scope.SystemRootEnv, system)

	return user, system
}

// newAppFixture lays the application with files out in appScope: a project
// directory with .tarantool in it, or a version under the user or the
// system root with apps/<app> linking to it.
func newAppFixture(t *testing.T, appScope app.Scope, files map[string]string) appFixture {
	t.Helper()

	user, system := setScopeRoots(t)

	switch appScope {
	case app.ScopeProject:
		dir := filepath.Join(t.TempDir(), fixtureApp)
		writeFiles(t, dir, files)

		return appFixture{scope: appScope, root: scope.ProjectRoot(dir), dir: dir, link: ""}
	case app.ScopeUser, app.ScopeSystem:
		root := user
		if appScope == app.ScopeSystem {
			root = system
		}

		dir, link := installVersion(t, root, fixtureVersion, files)

		return appFixture{scope: appScope, root: root, dir: dir, link: link}
	}

	require.FailNow(t, "unknown scope", appScope)

	return appFixture{}
}

// installVersion lays a version of the application out under root and points
// apps/<app> at it, replacing the link to an earlier version. It returns the
// version directory and the link.
func installVersion(t *testing.T, root, version string, files map[string]string) (
	string, string,
) {
	t.Helper()

	rel := filepath.Join("versions", fixtureApp, fixtureApp+"_"+version)
	dir := filepath.Join(root, rel)
	writeFiles(t, dir, files)

	link := filepath.Join(root, "apps", fixtureApp)
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))

	// Switch the link atomically, as tt does: a new link renamed over it.
	tmpLink := link + ".new"
	require.NoError(t, os.Symlink(filepath.Join("..", rel), tmpLink))
	require.NoError(t, os.Rename(tmpLink, link))

	return dir, link
}

// load loads the application of the fixture as the instance commands will.
func (fixture appFixture) load(t *testing.T) *app.App {
	t.Helper()

	loaded, err := app.Load(context.Background(), fixtureApp,
		app.Options{Scope: fixture.scope, Template: "", Start: fixture.dir})
	require.NoError(t, err)

	return loaded
}

// instanceCtx loads the application and builds the context of storage-1.
func (fixture appFixture) instanceCtx(t *testing.T) InstanceCtx {
	t.Helper()

	loaded := fixture.load(t)
	require.Len(t, loaded.Instances, 1)

	inst, err := NewInstanceCtx(loaded, loaded.Instances[0])
	require.NoError(t, err)

	return inst
}

// fakeTarantool writes an executable that stands for tarantool at path.
func fakeTarantool(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

// installTarantool installs a fake tarantool of version into the fixture's
// scope and returns its path. In a project it becomes the active tarantool
// of the project.
func (fixture appFixture) installTarantool(t *testing.T, version string) string {
	t.Helper()

	path := filepath.Join(fixture.root, "versions", "tarantool", "tarantool_"+version, "tarantool")
	fakeTarantool(t, path)

	if fixture.scope == app.ScopeProject {
		link := filepath.Join(fixture.root, "bin", "tarantool")
		require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
		require.NoError(t, os.Symlink(path, link))
	}

	return path
}

// envOf returns the value of the last assignment to name in env, the one a
// process gets.
func envOf(env []string, name string) (string, bool) {
	for _, assignment := range slices.Backward(env) {
		value, found := strings.CutPrefix(assignment, name+"=")
		if found {
			return value, true
		}
	}

	return "", false
}

// socketTarget returns the socket that tarantool, working in workDir, binds
// when it is given path, with the links resolved.
func socketTarget(workDir, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(physicalPath(workDir), path)
	}

	return physicalPath(path)
}

// TestNewInstanceCtxLayout pins where the runtime artifacts of an instance go
// in every scope, and that it runs in the application directory: the version
// directory, not the link, in the user and system scopes.
func TestNewInstanceCtxLayout(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, appFiles("", ""))
			inst := fixture.instanceCtx(t)

			if appScope == app.ScopeProject {
				assert.Equal(t, filepath.Join(fixture.dir, ".tarantool"), inst.Root)
			} else {
				target, err := filepath.EvalSymlinks(fixture.link)
				require.NoError(t, err)
				assert.Equal(t, target, inst.AppDir)
				assert.NotEqual(t, fixture.link, inst.AppDir)
			}

			data := filepath.Join(fixture.root, "var", "lib", fixtureApp, fixtureInst)
			logs := filepath.Join(fixture.root, "var", "log", fixtureApp, fixtureInst)
			run := filepath.Join(fixture.root, "var", "run", fixtureApp, fixtureInst)

			assert.Equal(t, fixture.dir, inst.AppDir)
			assert.Equal(t, appScope, inst.Scope)
			assert.Equal(t, fixture.root, inst.Root)
			assert.Equal(t, fixtureApp, inst.AppName)
			assert.Equal(t, fixtureInst, inst.InstName)
			assert.Equal(t, data, inst.WalDir)
			assert.Equal(t, data, inst.MemtxDir)
			assert.Equal(t, data, inst.VinylDir)
			assert.Equal(t, logs, inst.LogDir)
			assert.Equal(t, filepath.Join(logs, "tt.log"), inst.Log)
			assert.Equal(t, run, inst.RunDir)
			assert.Equal(t, filepath.Join(run, "tt.pid"), inst.PIDFile)
			assert.Equal(t, filepath.Join(run, "tarantool.control"), inst.ConsoleSocket)
			assert.Equal(t, filepath.Join(run, "tarantool.sock"), inst.BinaryPort)
			assert.Equal(t, filepath.Join(fixture.dir, ".templates", "default", "config.yaml"),
				inst.ClusterConfigPath)
			assert.NotNil(t, inst.Configuration)
			assert.Equal(t, fixtureApp+":"+fixtureInst, GetAppInstanceName(inst))
		})
	}
}

// TestNewInstanceCtxRecordSettings pins that the stop, restart and env
// settings of the record reach the context.
func TestNewInstanceCtxRecordSettings(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, appFiles(
		"{stop_signal: SIGTERM, stop_timeout: 1m, restart: on-failure, env: {A: b}}", ""))
	inst := fixture.instanceCtx(t)

	assert.Equal(t, syscall.SIGTERM, inst.StopSignal)
	assert.Equal(t, time.Minute, inst.StopTimeout)
	assert.Equal(t, instances.RestartOnFailure, inst.Restart)
	assert.Equal(t, map[string]string{"A": "b"}, inst.Env)

	fixture = newAppFixture(t, app.ScopeUser, appFiles("", ""))
	inst = fixture.instanceCtx(t)

	assert.Equal(t, syscall.Signal(0), inst.StopSignal)
	assert.Equal(t, time.Duration(0), inst.StopTimeout)
	assert.Equal(t, instances.RestartNo, inst.Restart)
}

// TestNewInstanceCtxConfigWins pins that the paths the config of the
// instance sets win over the layout, relative ones resolved against the
// application directory, and that the TT_ variables of the record win over
// the config, as they do in tarantool.
func TestNewInstanceCtxConfigWins(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, appFiles(
		"{env: {TT_VINYL_DIR: /env/vinyl}}",
		"wal:\n  dir: /data/{{ instance_name }}/wal\nsnapshot:\n  dir: snaps\n"+
			"vinyl:\n  dir: /config/vinyl\nconsole:\n  socket: run/{{ instance_name }}.control\n"))
	inst := fixture.instanceCtx(t)

	assert.Equal(t, "/data/storage-1/wal", inst.WalDir)
	assert.Equal(t, filepath.Join(fixture.dir, "snaps"), inst.MemtxDir)
	assert.Equal(t, "/env/vinyl", inst.VinylDir)
	assert.Equal(t, filepath.Join(fixture.dir, "run", "storage-1.control"), inst.ConsoleSocket)
}

// TestNewInstanceCtxUnsupportedKinds pins that the Tarantool instances that
// run their script are refused for now.
func TestNewInstanceCtxUnsupportedKinds(t *testing.T) {
	application := &app.App{
		Name:  fixtureApp,
		Scope: app.ScopeUser,
		Root:  "/root",
		Dir:   "/root/versions/myapp/myapp_1.0.0",
	}

	for name, record := range map[string]instances.Instance{
		"script": {Name: "legacy-1", Script: "/root/versions/myapp/myapp_1.0.0/init.lua"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewInstanceCtx(application, record)
			require.ErrorIs(t, err, ErrUnsupportedKind)
			assert.ErrorContains(t, err, record.Name)
		})
	}
}

func TestNewInstanceCtxErrors(t *testing.T) {
	record := instances.Instance{Name: fixtureInst, Restart: instances.RestartNo}

	_, err := NewInstanceCtx(nil, record)
	require.ErrorIs(t, err, errNoApplication)

	_, err = NewInstanceCtx(&app.App{Name: fixtureApp, Root: "rel", Dir: "/dir"}, record)
	require.ErrorIs(t, err, errNotAbsolute)

	_, err = NewInstanceCtx(&app.App{Name: fixtureApp, Root: "/root", Dir: "rel"}, record)
	require.ErrorIs(t, err, errNotAbsolute)
}

// TestAppClusterSpec pins the Spec of an instance in every scope: tarantool
// of the application runs in the application directory from the config of
// the pair, with the paths of the layout as absolute defaults.
func TestAppClusterSpec(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, appFiles("{env: {RECORD: set}}", ""))

			// A project runs on the tarantool it selected, which may differ
			// from the one its lock bundles at the price of a warning.
			version := fixtureTarantool
			if appScope == app.ScopeProject {
				version = "3.7.0"
			}

			tarantoolPath := fixture.installTarantool(t, version)
			inst := fixture.instanceCtx(t)

			var warnings []string

			opts := specOptions{
				integrity: nil,
				stdout:    io.Discard,
				stderr:    io.Discard,
				warn:      func(message string) { warnings = append(warnings, message) },
			}

			spec, err := instanceSpec(&inst, opts)
			require.NoError(t, err)

			assert.Equal(t, tarantoolPath, spec.Path)
			assert.Equal(t, []string{"-n", fixtureInst, "-c", inst.ClusterConfigPath}, spec.Args)
			assert.Equal(t, fixture.dir, spec.Dir)
			assert.Equal(t, syscall.SIGINT, spec.StopSignal)
			assert.True(t, spec.ForwardStopSignal)
			assert.Equal(t, instanceStopTimeout, spec.StopTimeout)

			data := filepath.Join(fixture.root, "var", "lib", fixtureApp, fixtureInst)
			run := filepath.Join(fixture.root, "var", "run", fixtureApp, fixtureInst)
			logs := filepath.Join(fixture.root, "var", "log", fixtureApp, fixtureInst)

			for name, want := range map[string]string{
				"PWD":                         fixture.dir,
				"TT_WAL_DIR_DEFAULT":          data,
				"TT_SNAPSHOT_DIR_DEFAULT":     data,
				"TT_VINYL_DIR_DEFAULT":        data,
				"TT_LOG_FILE_DEFAULT":         filepath.Join(logs, "tarantool.log"),
				"TT_PROCESS_PID_FILE_DEFAULT": filepath.Join(run, "tarantool.pid"),
				"RECORD":                      "set",
			} {
				value, found := envOf(spec.Env, name)
				if assert.True(t, found, name) {
					assert.Equal(t, want, value, name)
				}
			}

			// A socket path may be shortened to one relative to the working
			// directory, depending on how deep the temporary directory is.
			console, _ := envOf(spec.Env, "TT_CONSOLE_SOCKET_DEFAULT")
			assert.Equal(t, physicalPath(filepath.Join(run, "tarantool.control")),
				socketTarget(spec.Dir, console))

			listen, _ := envOf(spec.Env, "TT_IPROTO_LISTEN_DEFAULT")

			var uris []map[string]string

			require.NoError(t, json.Unmarshal([]byte(listen), &uris))
			require.Len(t, uris, 1)
			assert.Equal(t, physicalPath(filepath.Join(run, "tarantool.sock")),
				socketTarget(spec.Dir, strings.TrimPrefix(uris[0]["uri"], "unix/:")))

			if appScope == app.ScopeProject {
				require.Len(t, warnings, 1)
				assert.Contains(t, warnings[0], fixtureTarantool)
			} else {
				assert.Empty(t, warnings)
			}
		})
	}
}

// TestAppClusterSpecLayoutWinsOverRecordEnv pins that the env of a record
// cannot move the defaults tt lays out under the record's own instance.
func TestAppClusterSpecLayoutWinsOverRecordEnv(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, appFiles(
		"{env: {TT_WAL_DIR_DEFAULT: /elsewhere, PWD: /elsewhere}}", ""))
	fixture.installTarantool(t, fixtureTarantool)

	inst := fixture.instanceCtx(t)

	spec, err := appClusterSpec(&inst, specOptions{})
	require.NoError(t, err)

	wal, _ := envOf(spec.Env, "TT_WAL_DIR_DEFAULT")
	assert.Equal(t, inst.WalDir, wal)

	pwd, _ := envOf(spec.Env, "PWD")
	assert.Equal(t, fixture.dir, pwd)
}

// TestAppClusterSpecStopSettings pins that the stop settings of the record
// reach the Spec.
func TestAppClusterSpecStopSettings(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeSystem, appFiles(
		"{stop_signal: TERM, stop_timeout: 90s}", ""))
	fixture.installTarantool(t, fixtureTarantool)

	inst := fixture.instanceCtx(t)

	spec, err := instanceSpec(&inst, specOptions{})
	require.NoError(t, err)

	assert.Equal(t, syscall.SIGTERM, spec.StopSignal)
	assert.False(t, spec.ForwardStopSignal)
	assert.Equal(t, 90*time.Second, spec.StopTimeout)
}

// TestAppClusterSpecNoTarantool pins that an installed application without
// its tarantool does not start, and that its context still builds, so that
// its instances can be stopped and inspected.
func TestAppClusterSpecNoTarantool(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, appFiles("", ""))
	inst := fixture.instanceCtx(t)

	_, err := appClusterSpec(&inst, specOptions{})
	require.ErrorContains(t, err, "tt binary install")

	canStart, err := IsAbleToStartInstances([]InstanceCtx{inst})
	assert.False(t, canStart)
	require.Error(t, err)
}

// relativePathsConfig sets every path an installed instance must not have
// relative, but snapshot.dir, which the record sets through the env.
const relativePathsConfig = "wal:\n  dir: var/wal\nvinyl:\n  dir: /abs/vinyl\n" +
	"log:\n  to: file\n  file: logs/{{ instance_name }}.log\n" +
	"console:\n  socket: run/c.control\nprocess:\n  pid_file: run/t.pid\n"

// TestRelativePathsInInstalledScopes pins that a relative path the instance
// writes to is an error at start in the user and system scopes, naming every
// key, and resolves against the project directory in a project.
func TestRelativePathsInInstalledScopes(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, appFiles(
				"{env: {TT_SNAPSHOT_DIR: snaps}}", relativePathsConfig))
			fixture.installTarantool(t, fixtureTarantool)

			inst := fixture.instanceCtx(t)

			startErr := checkStart(&inst)
			_, specErr := appClusterSpec(&inst, specOptions{})
			canStart, ableErr := IsAbleToStartInstances([]InstanceCtx{inst})

			if appScope == app.ScopeProject {
				require.NoError(t, startErr)
				require.NoError(t, specErr)
				require.NoError(t, ableErr)
				assert.True(t, canStart)
				assert.Equal(t, filepath.Join(fixture.dir, "var", "wal"), inst.WalDir)
				assert.Equal(t, filepath.Join(fixture.dir, "snaps"), inst.MemtxDir)

				return
			}

			for _, err := range []error{startErr, specErr, ableErr} {
				require.ErrorIs(t, err, errRelativePath)

				for _, key := range []string{
					`wal.dir "var/wal"`, `snapshot.dir "snaps"`, `log.file "logs/`,
					`console.socket "run/c.control"`, `process.pid_file "run/t.pid"`,
				} {
					require.ErrorContains(t, err, key)
				}

				assert.NotContains(t, err.Error(), "vinyl.dir")
			}

			assert.False(t, canStart)
		})
	}
}

// TestOldLayoutGuard pins that a project with data in <project>/var/lib and
// none in <project>/.tarantool/var/lib does not start, and that refusing
// creates and moves nothing.
func TestOldLayoutGuard(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		fixture := newAppFixture(t, app.ScopeProject, appFiles("", ""))
		oldData := filepath.Join(fixture.dir, "var", "lib", fixtureInst)
		require.NoError(t, os.MkdirAll(oldData, 0o755))

		inst := fixture.instanceCtx(t)

		err := checkStart(&inst)
		require.ErrorIs(t, err, errOldLayout)
		require.ErrorContains(t, err, filepath.Join(fixture.dir, "var", "lib"))
		require.ErrorContains(t, err,
			filepath.Join(fixture.dir, ".tarantool", "var", "lib", fixtureApp, "<instance>"))

		err = RunInstance(context.Background(), &cmdcontext.CmdCtx{}, inst,
			io.Discard, io.Discard)
		require.ErrorIs(t, err, errOldLayout)

		err = Start(t.Context(), &cmdcontext.CmdCtx{}, &inst)
		require.ErrorIs(t, err, errOldLayout)

		assert.NoDirExists(t, fixture.root, "a refused start creates nothing")
		assert.DirExists(t, oldData, "a refused start moves nothing")
	})

	t.Run("new layout in use", func(t *testing.T) {
		fixture := newAppFixture(t, app.ScopeProject, appFiles("", ""))
		require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "var", "lib"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(fixture.root, "var", "lib"), 0o755))

		inst := fixture.instanceCtx(t)
		require.NoError(t, checkStart(&inst))
	})

	t.Run("data elsewhere by config", func(t *testing.T) {
		fixture := newAppFixture(t, app.ScopeProject, appFiles("",
			"wal:\n  dir: var/lib/{{ instance_name }}\n"+
				"snapshot:\n  dir: var/lib/{{ instance_name }}\n"+
				"vinyl:\n  dir: var/lib/{{ instance_name }}\n"))
		require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "var", "lib"), 0o755))

		inst := fixture.instanceCtx(t)
		require.NoError(t, checkStart(&inst))
	})

	t.Run("installed application", func(t *testing.T) {
		fixture := newAppFixture(t, app.ScopeUser, appFiles("", ""))
		require.NoError(t, os.MkdirAll(filepath.Join(fixture.dir, "var", "lib"), 0o755))

		inst := fixture.instanceCtx(t)
		require.NoError(t, checkStart(&inst))
	})
}

// longSocketApp returns an application in appScope under a root long enough
// that a socket path under it does not fit a unix socket address, with names
// of length nameLen.
func longSocketApp(appScope app.Scope, nameLen int) (*app.App, instances.Instance) {
	root := "/" + strings.Repeat("r", 90)
	name := strings.Repeat("n", nameLen)
	dir := filepath.Join(root, "versions", name, name+"_1.0.0")

	if appScope == app.ScopeProject {
		dir = filepath.Join(root, name)
		root = filepath.Join(dir, ".tarantool")
	}

	return &app.App{Name: name, Scope: appScope, Root: root, Dir: dir},
		instances.Instance{Name: name, Restart: instances.RestartNo}
}

// TestLongSocketPath pins that a socket path too long for a unix socket is
// given to tarantool relative to its working directory where tarantool takes
// it so: both sockets inside the working directory, the binary port out of it
// with "..". A console socket outside of the working directory, as in the
// user and system scopes, and a relative path still too long, are errors
// naming the path.
func TestLongSocketPath(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		application, record := longSocketApp(app.ScopeProject, 3)

		inst, err := NewInstanceCtx(application, record)
		require.NoError(t, err)
		require.Error(t, verifySocketLength(inst.ConsoleSocket), "the test needs a long path")

		run := ".tarantool/var/run/nnn/nnn"

		console, listen, err := socketArgs(&inst)
		require.NoError(t, err)
		assert.Equal(t, run+"/tarantool.control", console)
		assert.Equal(t, `[{"uri":"unix/:./`+run+`/tarantool.sock"}]`, listen)
		require.NoError(t, checkStart(&inst))
	})

	for _, appScope := range []app.Scope{app.ScopeUser, app.ScopeSystem} {
		t.Run(string(appScope), func(t *testing.T) {
			application, record := longSocketApp(appScope, 3)

			inst, err := NewInstanceCtx(application, record)
			require.NoError(t, err, "the context of an instance that cannot start builds")

			binaryPort, err := appSocketPath(inst.BinaryPort, inst.AppDir, true)
			require.NoError(t, err)
			assert.Equal(t, "../../../var/run/nnn/nnn/tarantool.sock", binaryPort)

			err = checkStart(&inst)
			require.ErrorIs(t, err, errSocketPathIsLongerThanSymbols)
			require.ErrorContains(t, err, "console socket")
			require.ErrorContains(t, err, inst.ConsoleSocket)
			require.ErrorContains(t, err, "outside of the working directory")
			require.ErrorContains(t, err, "shorten the scope root")
		})
	}

	t.Run("too long", func(t *testing.T) {
		application, record := longSocketApp(app.ScopeProject, 40)

		inst, err := NewInstanceCtx(application, record)
		require.NoError(t, err)

		err = checkStart(&inst)
		require.ErrorIs(t, err, errSocketPathIsLongerThanSymbols)
		require.ErrorContains(t, err, "console socket")
		require.ErrorContains(t, err, "shorten the scope root")
	})
}

// TestPhysicalPath pins that the relative socket path is taken between the
// physical paths, which the kernel follows.
func TestPhysicalPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "a"), 0o755))

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(target, link))

	resolved, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(resolved, "a"), physicalPath(filepath.Join(link, "a")))
	assert.Equal(t, filepath.Join(resolved, "a", "missing", "x"),
		physicalPath(filepath.Join(link, "a", "missing", "x")))
	assert.Equal(t, "/nonexistent-root/x", physicalPath("/nonexistent-root/x"))
}

// TestIsAbleToStartInstancesWarnsOnce pins that the tarantool of an
// application is resolved for every instance, and that its warnings are
// logged once for the application rather than for each instance.
func TestIsAbleToStartInstancesWarnsOnce(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeProject, appFiles("", ""))
	fixture.installTarantool(t, "3.7.0")

	first := fixture.instanceCtx(t)
	second := fixture.instanceCtx(t)

	second.InstName = "storage-2"

	logger, recorder := logtest.New(t)
	previous := slog.Default()

	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	canStart, err := IsAbleToStartInstances([]InstanceCtx{first, second})
	require.NoError(t, err)
	assert.True(t, canStart)

	records := recorder.Records()
	require.Len(t, records, 1)
	assert.Equal(t, slog.LevelWarn, records[0].Level)
	assert.Contains(t, records[0].Message, fixtureTarantool)
}
