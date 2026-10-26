package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/internal/systemd"
)

// systemdFixture installs the application myapp into a new system root,
// with tt of the scope, and runs the test outside of any project. The pair of
// the host describes a Tarantool instance configured by the cluster config
// with the defaults, another with restart and stop_timeout, an instance that
// runs its script and an external process. It returns the root.
func systemdFixture(t *testing.T) string {
	t.Helper()

	t.Setenv("TT_USER_ROOT", t.TempDir())

	root := t.TempDir()
	t.Setenv("TT_SYSTEM_ROOT", root)
	t.Setenv(systemd.UnitEnv, "")
	t.Chdir(t.TempDir())
	withProjectDir(t, "")

	topology := "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
		"          router-1: {}\n          storage-1: {}\n"
	version := filepath.Join("versions", "myapp", "myapp_1.0.0")

	for name, content := range map[string]string{
		filepath.Join(version, "init.lua"):                          "",
		filepath.Join(version, "bin", "gateway"):                    "#!/bin/sh\n",
		filepath.Join(version, ".templates/default/instances.yaml"): "router-1:\n",
		filepath.Join(version, ".templates/default/config.yaml"):    topology,
		filepath.Join("etc", "myapp", "config.yaml"):                topology,
		filepath.Join("bin", "tt"):                                  "#!/bin/sh\n",
		filepath.Join("etc", "myapp", "instances.yaml"): "router-1:\n" +
			"storage-1:\n  restart: on-failure\n  stop_timeout: 1m\n" +
			"legacy:\n  script: init.lua\n  restart: always\n  stop_timeout: 2500ms\n" +
			"gateway:\n  binary: ./bin/gateway\n  stop_signal: SIGINT\n  stop_timeout: 10s\n" +
			"  restart: on-failure\n",
	} {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	}

	require.NoError(t, os.MkdirAll(filepath.Join(root, "apps"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join("..", version),
		filepath.Join(root, "apps", "myapp")))

	return root
}

// captureStdout returns what run writes to the standard output.
func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()

	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	previous := os.Stdout

	os.Stdout = writer

	output := make(chan string)

	go func() {
		data, _ := io.ReadAll(reader)
		output <- string(data)
	}()

	runErr := run()

	os.Stdout = previous

	require.NoError(t, writer.Close())

	return <-output, runErr
}

// managedStatus returns the units tt instance status shows for the instances
// of myapp, by instance.
func managedStatus(t *testing.T) map[string]string {
	t.Helper()

	previous := opts

	opts = statusOpts{format: statusFormatJSON, details: false}

	t.Cleanup(func() { opts = previous })

	out, err := captureStdout(t, func() error {
		return internalStatusModule(&cmdcontext.CmdCtx{}, []string{"myapp:*"})
	})
	require.NoError(t, err)

	var statuses map[string]struct {
		Unit *string `json:"unit"`
	}

	require.NoError(t, json.Unmarshal([]byte(out), &statuses))

	units := map[string]string{}

	for name, state := range statuses {
		if state.Unit != nil {
			units[name] = *state.Unit
		}
	}

	return units
}

// TestRefuseManaged pins that the commands that start and stop instances
// refuse those that systemd units manage, as the mark of the units names
// them, before they act on any instance, and name the systemctl command to
// use instead; status shows the units.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestRefuseManaged(t *testing.T) {
	root := systemdFixture(t)
	cmdCtx := &cmdcontext.CmdCtx{}

	require.NoError(t, systemd.WriteMark(filepath.Join(root, "etc", "myapp"), systemd.Mark{
		UnitDir:  "/etc/systemd/system",
		Template: "myapp@.service",
		Instances: map[string]string{
			"storage-1": "myapp@storage-1.service", "gateway": "myapp@gateway.service",
		},
	}))

	assert.Equal(t, map[string]string{
		"myapp:storage-1": "myapp@storage-1.service", "myapp:gateway": "myapp@gateway.service",
	}, managedStatus(t))

	instanceYes = true

	t.Cleanup(func() { instanceYes = false })

	for name, module := range map[string]func(*cmdcontext.CmdCtx, []string) error{
		"start":                 internalStartModule,
		"stop":                  internalStopWithConfirmationModule,
		"restart":               internalRestartModule,
		"kill --signal=SIGKILL": internalKillModule,
	} {
		err := module(cmdCtx, []string{"myapp:storage-1"})
		require.ErrorIs(t, err, errManagedBySystemd, name)
		require.EqualError(t, err, "myapp:storage-1 is managed by systemd through the unit "+
			"myapp@storage-1.service: use systemctl "+name+" myapp@storage-1.service", name)

		// Nothing is acted on: not even the instance without a unit.
		err = module(cmdCtx, []string{"myapp:*"})
		require.ErrorIs(t, err, errManagedBySystemd, name)
		require.EqualError(t, err, "myapp:storage-1, myapp:gateway are managed by systemd "+
			"through their units: use systemctl "+name+" myapp@storage-1.service "+
			"myapp@gateway.service", name)
	}

	dumpQuit = true

	t.Cleanup(func() { dumpQuit = false })

	err := internalKillModule(cmdCtx, []string{"myapp:storage-1"})
	require.ErrorContains(t, err, "use systemctl kill --signal=SIGQUIT myapp@storage-1.service")

	err = internalReplicasetRebootstrapModule(cmdCtx, []string{"myapp:storage-1"})
	require.ErrorIs(t, err, errManagedBySystemd)
	require.ErrorContains(t, err, "stop it with systemctl stop myapp@storage-1.service, "+
		"remove its data with tt instance clean myapp:storage-1 --yes")

	pidFile, err := running.PIDFile(root, "myapp", "router-1")
	require.NoError(t, err)

	// An instance without a unit is acted on as before.
	require.NoError(t, internalStopWithConfirmationModule(cmdCtx, []string{"myapp:router-1"}))
	assert.NoFileExists(t, pidFile)
}

// TestStartedByItsUnit pins the one start of an instance of a unit that is
// not refused: tt instance start --foreground that the unit of the instance
// runs, which names itself in TT_SYSTEMD_UNIT.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestStartedByItsUnit(t *testing.T) {
	previous := startForeground

	t.Cleanup(func() { startForeground = previous })

	insts := []running.InstanceCtx{
		{AppName: "myapp", InstName: "a", Unit: "myapp@a.service"},
		{AppName: "myapp", InstName: "b", Unit: "myapp@b.service"},
		{AppName: "myapp", InstName: "c"},
	}

	cases := []struct {
		name       string
		foreground bool
		unit       string
		refused    string
	}{
		{"in the background", false, "myapp@a.service", "myapp:a, myapp:b are managed"},
		{"from no unit", true, "", "myapp:a, myapp:b are managed"},
		{"from the unit of a", true, "myapp@a.service", "myapp:b is managed"},
		{"from another unit", true, "other@a.service", "myapp:a, myapp:b are managed"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			startForeground = test.foreground

			t.Setenv(systemd.UnitEnv, test.unit)

			err := refuseManaged(insts, "start", startedByItsUnit)
			require.ErrorIs(t, err, errManagedBySystemd)
			require.ErrorContains(t, err, test.refused)
		})
	}

	startForeground = true

	t.Setenv(systemd.UnitEnv, "myapp@a.service")
	require.NoError(t, refuseManaged(insts[:1], "start", startedByItsUnit))
	require.NoError(t, refuseManaged(insts[2:], "start", nil))
}
