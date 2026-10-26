package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/running"
)

// externalProject lays out the project myapp, whose default template runs
// two Tarantool instances around worker-1, an external process, and makes
// it the -C directory.
func externalProject(t *testing.T) {
	t.Helper()

	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	dir := filepath.Join(t.TempDir(), "myapp")

	for name, content := range map[string]string{
		".templates/default/instances.yaml": "storage-1:\nworker-1:\n  binary: ./bin/worker\n" +
			"storage-2:\n",
		".templates/default/config.yaml": "groups:\n  g:\n    replicasets:\n      r:\n" +
			"        instances:\n          storage-1: {}\n          storage-2: {}\n",
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	withProjectDir(t, dir)
}

// instanceNames returns the names of insts.
func instanceNames(insts []running.InstanceCtx) []string {
	names := make([]string, 0, len(insts))
	for _, inst := range insts {
		names = append(names, inst.InstName)
	}

	return names
}

// TestResolveAppTargetExternal pins how tt replicaset and tt cluster treat an
// external process: it is no instance of the cluster, so it is left out of
// the instances of the application, and naming it is an error.
func TestResolveAppTargetExternal(t *testing.T) {
	externalProject(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	for target, wantInst := range map[string]string{
		"myapp": "", "myapp:*": "", "myapp:storage-2": "storage-2",
	} {
		application, insts, instName, err := resolveAppTarget(cmdCtx, target, readConfig)
		require.NoError(t, err, target)
		assert.Len(t, application.Instances, 3, "the description is whole")
		assert.Equal(t, []string{"storage-1", "storage-2"}, instanceNames(insts), target)
		assert.Equal(t, wantInst, instName, target)
	}

	application, insts, _, err := resolveAppTarget(cmdCtx, "myapp:worker-1", readConfig)
	require.ErrorIs(t, err, running.ErrNotTarantool)
	assert.Nil(t, application)
	assert.Nil(t, insts)

	_, _, err = parseAppStr(cmdCtx, "myapp:worker-1")
	require.ErrorIs(t, err, running.ErrNotTarantool)

	// tt cluster may configure an instance that does not run on this host.
	_, instName, err := parseAppStr(cmdCtx, "myapp:storage-3")
	require.NoError(t, err)
	assert.Equal(t, "storage-3", instName)
}

// TestTarantoolCommandsRefuseExternal pins that the commands that reach an
// instance through its sockets refuse an external process, while the
// instance commands find it.
func TestTarantoolCommandsRefuseExternal(t *testing.T) {
	externalProject(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	insts, err := resolveInstances(cmdCtx, []string{"myapp:worker-1"})
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.True(t, running.IsExternal(&insts[0]))

	_, err = resolveConnectOpts(cmdCtx, &connect.ConnectCtx{}, "myapp:worker-1")
	require.ErrorIs(t, err, running.ErrNotTarantool)

	err = internalPlayModule(cmdCtx, []string{"myapp:worker-1", "00000000000000000000.xlog"})
	require.ErrorIs(t, err, running.ErrNotTarantool)

	err = internalReplicasetRebootstrapModule(cmdCtx, []string{"myapp:worker-1"})
	require.ErrorIs(t, err, running.ErrNotTarantool)
}

// TestCleanExternal pins that tt instance clean removes the log of an external
// process and what it has written into its data directory, the working
// directory of a process without workdir.
func TestCleanExternal(t *testing.T) {
	externalProject(t)

	insts, err := resolveInstances(&cmdcontext.CmdCtx{}, []string{"myapp:worker-1"})
	require.NoError(t, err)
	require.Len(t, insts, 1)

	inst := insts[0]
	require.NotEmpty(t, inst.DataDir)
	require.Equal(t, inst.DataDir, inst.Workdir)

	state := filepath.Join(inst.DataDir, "state", "worker.state")
	require.NoError(t, os.MkdirAll(filepath.Dir(state), 0o755))
	require.NoError(t, os.WriteFile(state, []byte("1\n"), 0o644))
	require.NoError(t, os.MkdirAll(inst.LogDir, 0o755))
	require.NoError(t, os.WriteFile(inst.Log, []byte("worker started\n"), 0o644))

	require.NoError(t, clean(&inst))
	assert.NoFileExists(t, inst.Log)
	assert.NoFileExists(t, state)
}
