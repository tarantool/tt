package running

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
)

// pairFiles returns the files of an application whose default template
// runs the instances of description, a YAML instances description, from a
// cluster config declaring the names.
func pairFiles(description string, names ...string) map[string]string {
	var config strings.Builder

	config.WriteString("groups:\n  g:\n    replicasets:\n      r:\n        instances:\n")

	for _, name := range names {
		config.WriteString("          " + name + ": {}\n")
	}

	return map[string]string{
		"app.manifest.toml":                 fixtureManifest,
		"app.manifest.lock":                 fixtureLock,
		".templates/default/instances.yaml": description,
		".templates/default/config.yaml":    config.String(),
	}
}

// instNames returns the targets naming insts.
func instNames(insts []InstanceCtx) []string {
	names := make([]string, 0, len(insts))
	for _, inst := range insts {
		names = append(names, GetAppInstanceName(inst))
	}

	return names
}

// collect resolves the targets args spell with opts.
func collect(t *testing.T, opts app.Options, args ...string) ([]InstanceCtx, error) {
	t.Helper()

	targets, err := app.ParseTargets(args)
	require.NoError(t, err)

	return Collect(t.Context(), targets, opts)
}

// TestCollectProjectFromSubdirectory pins that the instances of a project
// are found from any directory inside it, with no tt.yaml anywhere: no
// target selects every instance of its description, in its order, and their
// artifacts go under .tarantool of the project.
func TestCollectProjectFromSubdirectory(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	writeFiles(t, dir, pairFiles("storage-2:\nstorage-1:\n", "storage-1", "storage-2"))

	sub := filepath.Join(dir, "src", "lib")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	for _, start := range []string{dir, sub} {
		insts, err := collect(t, app.Options{Start: start})
		require.NoError(t, err)

		assert.Equal(t, []string{"myapp:storage-2", "myapp:storage-1"}, instNames(insts))

		for _, inst := range insts {
			assert.Equal(t, app.ScopeProject, inst.Scope)
			assert.Equal(t, dir, inst.AppDir)
			assert.Equal(t, filepath.Join(dir, ".tarantool"), inst.Root)
			assert.Equal(t,
				filepath.Join(dir, ".tarantool", "var", "lib", fixtureApp, inst.InstName),
				inst.WalDir)
			assert.Equal(t,
				filepath.Join(dir, ".tarantool", "var", "run", fixtureApp, inst.InstName, "tt.pid"),
				inst.PIDFile)
		}
	}
}

// TestCollectTargets pins the targets of the instance commands: one
// instance, every instance, and lists, which keep the order of the
// description; and that the template a command was given stays with every
// instance for its watchdog.
func TestCollectTargets(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	writeFiles(t, dir, pairFiles("router:\nstorage-1:\nstorage-2:\n",
		"router", "storage-1", "storage-2"))

	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"myapp:storage-1"}, []string{"myapp:storage-1"}},
		{[]string{"myapp:*"}, []string{"myapp:router", "myapp:storage-1", "myapp:storage-2"}},
		{[]string{"myapp:storage-2,myapp:router"}, []string{"myapp:router", "myapp:storage-2"}},
		{[]string{"myapp:storage-2", "myapp:router"}, []string{"myapp:router", "myapp:storage-2"}},
	}

	for _, test := range cases {
		insts, err := collect(t, app.Options{Start: dir, Template: "default"}, test.args...)
		require.NoError(t, err)
		assert.Equal(t, test.want, instNames(insts), test.args)

		for _, inst := range insts {
			assert.Equal(t, "default", inst.Template)
		}
	}

	_, err := collect(t, app.Options{Start: dir}, "myapp:storage-3")
	require.ErrorIs(t, err, app.ErrInstanceNotFound)
}

// TestCollectSystemScope pins that an application installed in the system
// scope, at TT_SYSTEM_ROOT, is reached from anywhere by its name: it runs in
// its version directory and keeps its artifacts under the system root.
func TestCollectSystemScope(t *testing.T) {
	_, system := setScopeRoots(t)

	version, _ := installVersion(t, system, fixtureVersion,
		pairFiles("storage-1:\n", "storage-1"))
	version, err := filepath.EvalSymlinks(version)
	require.NoError(t, err)

	insts, err := collect(t, app.Options{Start: t.TempDir()}, "myapp:*")
	require.NoError(t, err)
	require.Len(t, insts, 1)

	inst := insts[0]
	assert.Equal(t, app.ScopeSystem, inst.Scope)
	assert.Equal(t, system, inst.Root)
	assert.Equal(t, version, inst.AppDir)
	assert.Equal(t, filepath.Join(system, "var", "lib", fixtureApp, fixtureInst), inst.WalDir)
	assert.Equal(t, filepath.Join(system, "var", "log", fixtureApp, fixtureInst, "tt.log"),
		inst.Log)
}

// TestCollectKinds pins that the commands find the instances of every kind
// among the instances of an application, in the order of the description.
func TestCollectKinds(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	writeFiles(t, dir, pairFiles(
		"storage-1:\nlegacy-1:\n  script: init.lua\nworker-1:\n  binary: ./bin/worker\n",
		"storage-1"))

	insts, err := collect(t, app.Options{Start: dir}, "myapp:*")
	require.NoError(t, err)
	assert.Equal(t, []string{"myapp:storage-1", "myapp:legacy-1", "myapp:worker-1"},
		instNames(insts))
	assert.Equal(t, []instances.Kind{
		instances.KindCluster, instances.KindScript, instances.KindExternal,
	}, []instances.Kind{insts[0].Kind, insts[1].Kind, insts[2].Kind})

	insts, err = collect(t, app.Options{Start: dir}, "myapp:legacy-1")
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.True(t, IsScript(&insts[0]))
	assert.Equal(t, filepath.Join(dir, "init.lua"), insts[0].InstanceScript)
}

// TestReloadStaysOnPinnedVersion pins what the watchdog reads before it
// starts an instance again: the pair of the version the instance started
// from, read anew, even after apps/<app> has moved to another version; while
// resolving the application anew, as an explicit restart does, finds the
// active version.
func TestReloadStaysOnPinnedVersion(t *testing.T) {
	user, _ := setScopeRoots(t)

	first, _ := installVersion(t, user, "1.0.0", pairFiles("storage-1:\n", "storage-1"))
	first, err := filepath.EvalSymlinks(first)
	require.NoError(t, err)

	opts := app.Options{Scope: app.ScopeUser, Start: t.TempDir()}

	insts, err := collect(t, opts, "myapp:storage-1")
	require.NoError(t, err)
	require.Len(t, insts, 1)

	started := insts[0]
	assert.Equal(t, instances.RestartNo, started.Restart)

	second, _ := installVersion(t, user, "2.0.0", pairFiles("storage-1:\n", "storage-1"))

	second, err = filepath.EvalSymlinks(second)
	require.NoError(t, err)

	// The pair of the running version changes.
	writeFiles(t, first, map[string]string{
		".templates/default/instances.yaml": "storage-1: {restart: on-failure}\n",
	})

	reloaded, err := reload(t.Context(), &started, integrity.IntegrityCtx{Repository: nil})
	require.NoError(t, err)
	assert.Equal(t, first, reloaded.AppDir, "a restart stays on its version")
	assert.Equal(t, instances.RestartOnFailure, reloaded.Restart, "the pair is read anew")
	assert.Equal(t, started.PIDFile, reloaded.PIDFile)

	resolved, err := collect(t, opts, "myapp:storage-1")
	require.NoError(t, err)
	assert.Equal(t, second, resolved[0].AppDir, "resolving anew finds the active version")
}

// TestReloadProject pins that the watchdog of a project instance finds its
// project from the directory it started in, wherever it runs, and fails
// when the instance has left the description.
func TestReloadProject(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	writeFiles(t, dir, pairFiles("storage-1:\n", "storage-1"))

	insts, err := collect(t, app.Options{Start: dir})
	require.NoError(t, err)

	t.Chdir(t.TempDir())

	reloaded, err := reload(t.Context(), &insts[0], integrity.IntegrityCtx{Repository: nil})
	require.NoError(t, err)
	assert.Equal(t, insts[0].WalDir, reloaded.WalDir)

	writeFiles(t, dir, pairFiles("storage-2:\n", "storage-2"))

	_, err = reload(t.Context(), &insts[0], integrity.IntegrityCtx{Repository: nil})
	require.ErrorIs(t, err, errNotReloaded)
	require.ErrorIs(t, err, app.ErrInstanceNotFound)
}

// TestReloadReadsConfig pins that the watchdog requires the cluster config
// of its pair when it reads the instance anew, whatever the context it was
// given was built with: the commands that only stop and inspect instances
// read the config as far as they can (app.Options.BestEffortConfig), the
// start of an instance does not do without it.
func TestReloadReadsConfig(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	wal := filepath.Join(t.TempDir(), "wal")
	files := pairFiles("storage-1:\n", "storage-1")
	config := files[".templates/default/config.yaml"] + "wal:\n  dir: " + wal + "\n"

	files[".templates/default/config.yaml"] = "groups: [\n"
	writeFiles(t, dir, files)

	insts, err := collect(t, app.Options{Start: dir, BestEffortConfig: true})
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.Nil(t, insts[0].Configuration)
	assert.NotEqual(t, wal, insts[0].WalDir, "the layout without the config")

	_, err = reload(t.Context(), &insts[0], integrity.IntegrityCtx{Repository: nil})
	require.ErrorIs(t, err, errNotReloaded)

	writeFiles(t, dir, map[string]string{".templates/default/config.yaml": config})

	reloaded, err := reload(t.Context(), &insts[0], integrity.IntegrityCtx{Repository: nil})
	require.NoError(t, err)
	assert.NotNil(t, reloaded.Configuration)
	assert.Equal(t, wal, reloaded.WalDir, "the config has the last word")
}

// TestCollectBestEffortConfig pins the contexts of the instances of a pair
// whose config is read as far as it can be: a config that can be read has
// the last word on the paths it sets, the console socket among them; an
// instance it does not declare, or does not fit, and every instance of a
// config that cannot be read get the paths of the layout.
func TestCollectBestEffortConfig(t *testing.T) {
	setScopeRoots(t)

	dir := filepath.Join(t.TempDir(), fixtureApp)
	socket := filepath.Join(t.TempDir(), "console.sock")
	topology := "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
		"          storage-1:\n            console:\n              socket: " + socket + "\n" +
		"          storage-2:\n            wal:\n              dir: 5\n"
	files := pairFiles("storage-1:\nstorage-2:\nstorage-3:\n")

	files[".templates/default/config.yaml"] = topology
	writeFiles(t, dir, files)

	opts := app.Options{Start: dir, BestEffortConfig: true}

	insts, err := collect(t, opts)
	require.NoError(t, err)
	require.Len(t, insts, 3)

	layout, err := collect(t, app.Options{Start: dir, BestEffortConfig: true}, "myapp:storage-3")
	require.NoError(t, err)

	layoutSocket := func(inst string) string {
		return strings.Replace(layout[0].ConsoleSocket, "storage-3", inst, 1)
	}

	assert.Equal(t, socket, insts[0].ConsoleSocket, "the config moves the socket")
	assert.NotNil(t, insts[0].Configuration)
	assert.Equal(t, layoutSocket("storage-2"), insts[1].ConsoleSocket, "a config that does not fit")
	assert.Nil(t, insts[1].Configuration)
	assert.Equal(t, layoutSocket("storage-3"), insts[2].ConsoleSocket, "an instance it lacks")
	assert.Nil(t, insts[2].Configuration)

	_, err = collect(t, app.Options{Start: dir})
	require.ErrorIs(t, err, app.ErrTopologyMismatch, "the config is required without it")

	writeFiles(t, dir, map[string]string{".templates/default/config.yaml": "groups: [\n"})

	insts, err = collect(t, opts)
	require.NoError(t, err)
	assert.Equal(t, layoutSocket("storage-1"), insts[0].ConsoleSocket, "a config not read")
}
