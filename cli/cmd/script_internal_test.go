package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/replicaset"
	"github.com/tarantool/tt/v3/cli/running"
)

// scriptProject lays out the project myapp with files, relative to the
// project directory, and makes it the -C directory. It returns the project
// directory.
func scriptProject(t *testing.T, files map[string]string) string {
	t.Helper()

	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	dir := filepath.Join(t.TempDir(), "myapp")

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	withProjectDir(t, dir)

	return dir
}

// mixedFiles are the files of a project whose default template runs a
// Tarantool instance of the cluster config, an instance that runs its
// script, and an external process.
func mixedFiles() map[string]string {
	return map[string]string{
		".templates/default/instances.yaml": "storage-1:\nlegacy-1:\n  script: init.lua\n" +
			"worker-1:\n  binary: ./bin/worker\n",
		".templates/default/config.yaml": "groups:\n  g:\n    replicasets:\n      r:\n" +
			"        instances:\n          storage-1: {}\n",
		"init.lua": "box.cfg{}\n",
	}
}

// scriptFiles are the files of a project whose default template runs two
// instances that run their scripts and an external process, without a
// cluster config.
func scriptFiles() map[string]string {
	return map[string]string{
		".templates/default/instances.yaml": "legacy-1:\n  script: init.lua\n" +
			"legacy-2:\n  script: init.lua\nworker-1:\n  binary: ./bin/worker\n",
		"init.lua": "box.cfg{}\n",
	}
}

// TestResolveAppTargetScriptsNextToCluster pins that tt replicaset and
// tt cluster leave out an instance that runs its script next to the
// instances of the cluster config, which the centralized config orchestrator
// acts on, and refuse a target that names one.
func TestResolveAppTargetScriptsNextToCluster(t *testing.T) {
	scriptProject(t, mixedFiles())

	cmdCtx := &cmdcontext.CmdCtx{}

	for _, target := range []string{"myapp", "myapp:*"} {
		application, insts, _, err := resolveAppTarget(cmdCtx, target)
		require.NoError(t, err, target)
		assert.Len(t, application.Instances, 3, "the description is whole")
		assert.Equal(t, []string{"storage-1"}, instanceNames(insts), target)
	}

	application, insts, _, err := resolveAppTarget(cmdCtx, "myapp:legacy-1")
	require.ErrorIs(t, err, running.ErrNotClusterInstance)
	assert.Nil(t, application)
	assert.Nil(t, insts)

	application, insts, _, err = resolveAppTarget(cmdCtx, "myapp:worker-1")
	require.ErrorIs(t, err, running.ErrNotTarantool)
	assert.Nil(t, application)
	assert.Nil(t, insts)
}

// TestResolveAppTargetScriptsAlone pins that an application of instances
// that run their scripts alone keeps them for tt replicaset, which acts on
// them with the custom orchestrator, as tt2 did; external processes are
// still left out.
func TestResolveAppTargetScriptsAlone(t *testing.T) {
	scriptProject(t, scriptFiles())

	cmdCtx := &cmdcontext.CmdCtx{}

	_, insts, instName, err := resolveAppTarget(cmdCtx, "myapp")
	require.NoError(t, err)
	assert.Equal(t, []string{"legacy-1", "legacy-2"}, instanceNames(insts))
	assert.Empty(t, instName)

	_, insts, instName, err = resolveAppTarget(cmdCtx, "myapp:legacy-2")
	require.NoError(t, err)
	assert.Len(t, insts, 2)
	assert.Equal(t, "legacy-2", instName)

	application, insts, _, err := resolveAppTarget(cmdCtx, "myapp:worker-1")
	require.ErrorIs(t, err, running.ErrNotTarantool)
	assert.Nil(t, application)
	assert.Nil(t, insts)
}

// TestClusterTargetsOfScripts pins what tt cluster show and publish do with
// the application targets of an application with instances that run their
// scripts: naming such an instance is an error, and an application without
// a config has none to show or to publish into, while one with a config
// still has it.
func TestClusterTargetsOfScripts(t *testing.T) {
	t.Run("next to the cluster", func(t *testing.T) {
		dir := scriptProject(t, mixedFiles())

		cmdCtx := &cmdcontext.CmdCtx{}

		_, _, err := parseAppStr(cmdCtx, "myapp:legacy-1")
		require.ErrorIs(t, err, running.ErrNotClusterInstance)

		path, instName, err := parseAppStr(cmdCtx, "myapp:storage-1")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, ".templates", "default", "config.yaml"), path)
		assert.Equal(t, "storage-1", instName)
	})

	t.Run("without a config", func(t *testing.T) {
		dir := scriptProject(t, scriptFiles())

		cmdCtx := &cmdcontext.CmdCtx{}

		for _, target := range []string{"myapp", "myapp:storage-1"} {
			_, _, err := parseAppStr(cmdCtx, target)
			require.ErrorIs(t, err, errNoClusterConfig, target)
			require.ErrorContains(t, err, "tt does not create it", target)
		}

		_, _, err := parseAppStr(cmdCtx, "myapp:legacy-1")
		require.ErrorIs(t, err, running.ErrNotClusterInstance)

		err = internalClusterPublishModule(cmdCtx, []string{"myapp", writeSource(t)})
		require.ErrorIs(t, err, errNoClusterConfig)
		assert.NoFileExists(t, filepath.Join(dir, ".templates", "default", "config.yaml"))
	})

	t.Run("with a config nothing reads", func(t *testing.T) {
		files := scriptFiles()

		files[".templates/default/config.yaml"] = "credentials: {}\n"

		dir := scriptProject(t, files)

		path, _, err := parseAppStr(&cmdcontext.CmdCtx{}, "myapp")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, ".templates", "default", "config.yaml"), path)
	})
}

// writeSource writes a cluster config to publish and returns its path.
func writeSource(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "source.yaml")
	require.NoError(t, os.WriteFile(path, []byte("groups:\n  g:\n    replicasets:\n"+
		"      r:\n        instances:\n          storage-1: {}\n"), 0o644))

	return path
}

// TestInstanceCommandsOfScripts pins that the commands that reach an
// instance through its sockets take an instance that runs its script, and
// that tt clean removes its data and its log.
func TestInstanceCommandsOfScripts(t *testing.T) {
	scriptProject(t, scriptFiles())

	cmdCtx := &cmdcontext.CmdCtx{}

	insts, err := resolveInstances(cmdCtx, []string{"myapp:legacy-1"})
	require.NoError(t, err)
	require.Len(t, insts, 1)

	inst := insts[0]
	require.True(t, running.IsScript(&inst))

	connOpts, err := resolveConnectOpts(cmdCtx, &connect.ConnectCtx{}, "myapp:legacy-1")
	require.NoError(t, err)
	assert.Equal(t, inst.ConsoleSocket, connOpts.Address)

	previous := forceRemove

	forceRemove = true

	t.Cleanup(func() { forceRemove = previous })

	snapshot := filepath.Join(inst.MemtxDir, "00000000000000000000.snap")
	require.NoError(t, os.MkdirAll(inst.MemtxDir, 0o755))
	require.NoError(t, os.WriteFile(snapshot, []byte("SNAP\n"), 0o644))
	require.NoError(t, os.MkdirAll(inst.LogDir, 0o755))
	require.NoError(t, os.WriteFile(inst.Log, []byte("started\n"), 0o644))

	require.NoError(t, clean(&inst))
	assert.NoFileExists(t, snapshot)
	assert.NoFileExists(t, inst.Log)
}

// TestAppOrchestrator pins the orchestrator of an application target of
// tt replicaset: the one the flags force, else the custom orchestrator for an
// application without Tarantool instances of the cluster config, else none,
// which leaves it to the version of tarantool.
func TestAppOrchestrator(t *testing.T) {
	load := func(files map[string]string) *app.App {
		scriptProject(t, files)

		application, err := loadApp(&cmdcontext.CmdCtx{}, "myapp")
		require.NoError(t, err)

		return application
	}

	scripts := load(scriptFiles())
	mixed := load(mixedFiles())

	for _, test := range []struct {
		name        string
		forced      replicaset.Orchestrator
		application *app.App
		want        replicaset.Orchestrator
	}{
		{"scripts", replicaset.OrchestratorUnknown, scripts, replicaset.OrchestratorCustom},
		{"mixed", replicaset.OrchestratorUnknown, mixed, replicaset.OrchestratorUnknown},
		{
			"scripts with --config", replicaset.OrchestratorCentralizedConfig, scripts,
			replicaset.OrchestratorCentralizedConfig,
		},
		{
			"mixed with --custom", replicaset.OrchestratorCustom, mixed,
			replicaset.OrchestratorCustom,
		},
	} {
		assert.Equal(t, test.want, appOrchestrator(test.forced, test.application), test.name)
	}
}

// TestReplicasetFillCtxScripts pins that tt replicaset acts on an
// application of instances that run their scripts with the custom
// orchestrator without --custom, and that a flag still forces another.
func TestReplicasetFillCtxScripts(t *testing.T) {
	scriptProject(t, scriptFiles())

	var ctx replicasetCtx

	require.NoError(t, replicasetFillCtx(&cmdcontext.CmdCtx{}, &ctx, "myapp", false))
	assert.Equal(t, replicaset.OrchestratorCustom, ctx.Orchestrator)
	assert.Equal(t, []string{"legacy-1", "legacy-2"}, instanceNames(ctx.RunningCtx.Instances))

	previous := orchestratorCentralizedConfig

	orchestratorCentralizedConfig = true

	t.Cleanup(func() { orchestratorCentralizedConfig = previous })

	ctx = replicasetCtx{}

	require.NoError(t, replicasetFillCtx(&cmdcontext.CmdCtx{}, &ctx, "myapp", false))
	assert.Equal(t, replicaset.OrchestratorCentralizedConfig, ctx.Orchestrator)
}
