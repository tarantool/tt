package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// unreachableConfig returns a thin cluster config that sends tt for the rest
// of the config to an etcd nobody listens on.
func unreachableConfig(t *testing.T) string {
	t.Helper()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return fmt.Sprintf("config:\n  etcd:\n    endpoints:\n      - http://%s\n"+
		"    prefix: /myapp\n    http:\n      request:\n        timeout: 0.5\n", address)
}

// configlessProject lays out the project myapp of two Tarantool instances,
// storage-1 and storage-2, whose cluster config is config, and makes it the
// -C directory and the working directory.
func configlessProject(t *testing.T, config string) {
	t.Helper()

	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	dir := filepath.Join(t.TempDir(), "myapp")

	for name, content := range map[string]string{
		".templates/default/instances.yaml": "storage-1:\nstorage-2:\n",
		".templates/default/config.yaml":    config,
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	withProjectDir(t, dir)
	t.Chdir(dir)
}

// fakeWatchdog runs a process that stands for the watchdog of inst, in a
// process group of its own, and writes its pid file. The process is reaped
// as soon as it exits, so that a stop sees it gone.
func fakeWatchdog(t *testing.T, inst *running.InstanceCtx) int {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sleep", "60")

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())

	done := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-done
	})

	require.NoError(t, os.MkdirAll(filepath.Dir(inst.PIDFile), 0o755))
	require.NoError(t, os.WriteFile(inst.PIDFile, []byte(strconv.Itoa(cmd.Process.Pid)),
		0o644))

	return cmd.Process.Pid
}

// statusOf returns the statuses tt instance status prints for the instances
// of myapp in JSON, by instance.
func statusOf(t *testing.T) map[string]string {
	t.Helper()

	previous := opts

	opts = statusOpts{format: statusFormatJSON, details: false}

	t.Cleanup(func() { opts = previous })

	out, err := captureStdout(t, func() error {
		return internalStatusModule(&cmdcontext.CmdCtx{}, []string{"myapp:*"})
	})
	require.NoError(t, err)

	var statuses map[string]struct {
		Status string `json:"status"`
	}

	require.NoError(t, json.Unmarshal([]byte(out), &statuses))

	got := map[string]string{}
	for name, state := range statuses {
		got[name] = state.Status
	}

	return got
}

// TestProcessCommandsWithoutConfig pins that tt instance stop, kill, status,
// logrotate and logs, the completion of a target and tt replicaset status
// find the instances of a pair whose cluster config cannot be read, a thin
// config pointing at an etcd that is down or a config broken by an edit, by
// the description and the pid files, and act on the running ones; while
// tt instance start, which needs the config, fails on it.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestProcessCommandsWithoutConfig(t *testing.T) {
	for name, config := range map[string]func(t *testing.T) string{
		"etcd out of reach": unreachableConfig,
		"broken config":     func(*testing.T) string { return "groups: [\n" },
	} {
		t.Run(name, func(t *testing.T) {
			configlessProject(t, config(t))

			cmdCtx := &cmdcontext.CmdCtx{}

			_, err := resolveInstances(cmdCtx, nil)
			require.Error(t, err, "the commands that need the config read it")

			insts, err := resolveProcesses(cmdCtx, nil)
			require.NoError(t, err)
			require.Equal(t, []string{"storage-1", "storage-2"}, instanceNames(insts))

			fakeWatchdog(t, &insts[0])

			assert.Equal(t, map[string]string{
				"myapp:storage-1": "RUNNING", "myapp:storage-2": "NOT RUNNING",
			}, statusOf(t))

			stop, _, err := NewInstanceCmd().Find([]string{"stop"})
			require.NoError(t, err)

			got, _ := internal.ValidArgsFunction(cmdCtx, stop, "myapp:", internal.Active)
			assert.Equal(t, []string{"myapp:storage-1"}, got)

			// The constructor has bound --yes to it, and reset it.
			instanceYes = true

			t.Cleanup(func() { instanceYes = false })

			require.NoError(t, internalLogsModule(cmdCtx, []string{"myapp:*"}))
			require.NoError(t, internalLogrotateModule(cmdCtx, []string{"myapp:storage-1"}))

			pid := fakeWatchdog(t, &insts[0])
			require.NoError(t, internalStopWithConfirmationModule(cmdCtx,
				[]string{"myapp:storage-1"}))
			require.Error(t, syscall.Kill(pid, 0), "the watchdog is stopped")

			fakeWatchdog(t, &insts[1])
			require.NoError(t, internalKillModule(cmdCtx, []string{"myapp:storage-2"}))
			assert.NoFileExists(t, insts[1].PIDFile)

			_, replicas, _, err := resolveAppTarget(cmdCtx, "myapp", readConfig)
			require.Error(t, err)
			assert.Empty(t, replicas)

			_, replicas, _, err = resolveAppTarget(cmdCtx, "myapp", tryConfig)
			require.NoError(t, err)
			assert.Equal(t, []string{"storage-1", "storage-2"}, instanceNames(replicas))
		})
	}
}

// TestProcessCommandsReadConfig pins that the commands that act on the
// processes of the instances still take what a config that can be read
// says: the console socket it moves out of the layout, which
// tt instance status and tt replicaset status ask an instance over.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestProcessCommandsReadConfig(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "storage-1.control")

	configlessProject(t, "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n"+
		"          storage-1:\n            console:\n              socket: "+socket+"\n"+
		"          storage-2: {}\n")

	cmdCtx := &cmdcontext.CmdCtx{}

	insts, err := resolveProcesses(cmdCtx, nil)
	require.NoError(t, err)
	require.Len(t, insts, 2)
	assert.Equal(t, socket, insts[0].ConsoleSocket)
	assert.NotEqual(t, socket, insts[1].ConsoleSocket)

	_, replicas, _, err := resolveAppTarget(cmdCtx, "myapp", tryConfig)
	require.NoError(t, err)
	require.Len(t, replicas, 2)
	assert.Equal(t, socket, replicas[0].ConsoleSocket)
}
