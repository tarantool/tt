package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/internal/systemd"
)

// systemdGolden is the golden file of TestSystemdGenerateGolden.
const systemdGolden = "testdata/systemd_generate.golden"

// setServiceFlags sets the flags of tt service systemd for the test.
func setServiceFlags(t *testing.T, scope, generateDir, installDir string) {
	t.Helper()

	previousScope, previousGenerate, previousInstall := serviceScope, generateUnitDir,
		installUnitDir
	previousGenerateRunAs, previousInstallRunAs := *generateRunAs, *installRunAs

	t.Cleanup(func() {
		serviceScope, generateUnitDir, installUnitDir = previousScope, previousGenerate,
			previousInstall
		*generateRunAs, *installRunAs = previousGenerateRunAs, previousInstallRunAs
	})

	serviceScope, generateUnitDir, installUnitDir = scope, generateDir, installDir
	*generateRunAs, *installRunAs = *newRunAsValue(), *newRunAsValue()
}

// TestServiceGroup pins the commands of tt service systemd and their flags.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestServiceGroup(t *testing.T) {
	top := make([]string, 0, len(BuiltinCommands()))
	for _, command := range BuiltinCommands() {
		top = append(top, command.Name())
	}

	assert.Contains(t, top, "service")

	systemdCmd, _, err := NewServiceCmd().Find([]string{"systemd"})
	require.NoError(t, err)

	names := make([]string, 0, len(systemdCmd.Commands()))
	for _, sub := range systemdCmd.Commands() {
		names = append(names, sub.Name())

		assert.NotNil(t, sub.Flags().Lookup("scope"), sub.Name())
	}

	assert.Equal(t, []string{"generate", "install", "uninstall"}, names)

	find := func(name string) *cobra.Command {
		sub, _, err := systemdCmd.Find([]string{name})
		require.NoError(t, err)

		return sub
	}

	assert.Empty(t, find("generate").Flags().Lookup("unit-dir").DefValue)
	assert.Equal(t, "/etc/systemd/system", find("install").Flags().Lookup("unit-dir").DefValue)
	assert.Nil(t, find("uninstall").Flags().Lookup("unit-dir"), "uninstall takes the mark's")

	for _, name := range []string{"generate", "install"} {
		runAs := find(name).Flags().Lookup("run-as")
		require.NotNil(t, runAs, name)
		assert.Equal(t, "tarantool:tarantool", runAs.DefValue, name)
		assert.Equal(t, "user[:group]", runAs.Value.Type(), name)

		require.ErrorIs(t, find(name).Flags().Set("run-as", "tt svc"), systemd.ErrUnitAccount)
	}

	assert.Nil(t, find("uninstall").Flags().Lookup("run-as"))
}

// TestSystemdGenerateGolden pins the units of an application with different
// restart and stop_timeout, an instance that runs its script and an external
// process among them, as tt service systemd generate prints them.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdGenerateGolden(t *testing.T) {
	// The fixture changes the working directory.
	golden, err := filepath.Abs(systemdGolden)
	require.NoError(t, err)

	root := systemdFixture(t)
	setServiceFlags(t, "", "", systemUnitDir)

	out, err := captureStdout(t, func() error {
		return internalSystemdGenerateModule(&cmdcontext.CmdCtx{}, []string{"myapp"})
	})
	require.NoError(t, err)

	got := strings.ReplaceAll(out, root, "<root>")

	if *update {
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
	}

	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// TestSystemdGenerateWithoutScopedTT pins that the units run the tt that
// makes them when the system scope has no tt of its own.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdGenerateWithoutScopedTT(t *testing.T) {
	root := systemdFixture(t)
	dir := t.TempDir()

	setServiceFlags(t, "", dir, systemUnitDir)
	require.NoError(t, os.Remove(filepath.Join(root, "bin", "tt")))

	require.NoError(t, internalSystemdGenerateModule(&cmdcontext.CmdCtx{}, []string{"myapp"}))

	self, err := os.Executable()
	require.NoError(t, err)

	unit, err := os.ReadFile(filepath.Join(dir, "myapp@.service"))
	require.NoError(t, err)
	assert.Contains(t, string(unit), "\nExecStart="+self+" instance start --foreground ")

	assert.FileExists(t, filepath.Join(dir, "myapp@gateway.service.d", "tt.conf"))
	assert.NoFileExists(t, systemd.MarkPath(filepath.Join(root, "etc", "myapp")),
		"generate records nothing")
}

// TestSystemdScopes pins that units are made for the system scope only: the
// user scope and projects are refused, and a target names a project as the
// instance commands do.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdScopes(t *testing.T) {
	systemdFixture(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	for _, scope := range []string{"user", "project"} {
		setServiceFlags(t, scope, t.TempDir(), t.TempDir())

		for _, module := range []func(*cmdcontext.CmdCtx, []string) error{
			internalSystemdGenerateModule, internalSystemdInstallModule,
			internalSystemdUninstallModule,
		} {
			err := module(cmdCtx, []string{"myapp"})
			if scope == "project" {
				// There is no project to look into.
				require.Error(t, err, scope)

				continue
			}

			require.ErrorIs(t, err, errScopeNotSupported, scope)
		}
	}

	project := filepath.Join(t.TempDir(), "myapp")
	require.NoError(t, os.MkdirAll(filepath.Join(project, ".templates", "default"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, ".templates", "default",
		"instances.yaml"), []byte("worker:\n  binary: sh\n"), 0o644))
	t.Chdir(project)

	setServiceFlags(t, "", t.TempDir(), t.TempDir())

	err := internalSystemdGenerateModule(cmdCtx, []string{"myapp"})
	require.ErrorIs(t, err, errScopeNotSupported)
	require.ErrorContains(t, err, "pass --scope system")

	setServiceFlags(t, "system", t.TempDir(), t.TempDir())
	require.NoError(t, internalSystemdGenerateModule(cmdCtx, []string{"myapp"}))
}

// TestSystemdNotInstalled pins the error for an application the system scope
// does not have: it points at tt package install, not at another scope.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdNotInstalled(t *testing.T) {
	root := systemdFixture(t)

	setServiceFlags(t, "", t.TempDir(), t.TempDir())

	for _, module := range []func(*cmdcontext.CmdCtx, []string) error{
		internalSystemdGenerateModule, internalSystemdInstallModule,
	} {
		err := module(&cmdcontext.CmdCtx{}, []string{"other"})
		require.ErrorIs(t, err, app.ErrAppNotFound)
		require.EqualError(t, err, `application not found: "other" is not installed in the `+
			"system scope in "+root+"; install it with tt package install --scope system "+
			"<ARCHIVE>")
	}
}

// TestSystemdInstallUninstall pins install and uninstall: the units go to the
// unit directory and the mark to the host files; the instance commands that
// start and stop instances refuse those of the units, pointing at systemctl,
// and status shows the units; uninstall takes both away.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdInstallUninstall(t *testing.T) {
	root := systemdFixture(t)
	unitDir := t.TempDir()
	cmdCtx := &cmdcontext.CmdCtx{}

	setServiceFlags(t, "", "", unitDir)

	out, err := captureStdout(t, func() error {
		return internalSystemdInstallModule(cmdCtx, []string{"myapp"})
	})
	require.NoError(t, err)
	assert.Contains(t, out, "systemctl daemon-reload\n")
	assert.Contains(t, out, "systemctl enable --now myapp@router-1.service "+
		"myapp@storage-1.service myapp@legacy.service myapp@gateway.service\n")

	assert.FileExists(t, filepath.Join(unitDir, "myapp@.service"))
	assert.FileExists(t, filepath.Join(unitDir, "myapp@legacy.service.d", "tt.conf"))

	hostDir := filepath.Join(root, "etc", "myapp")

	mark, found, err := systemd.ReadMark(hostDir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, systemd.Mark{
		UnitDir:  unitDir,
		Template: "myapp@.service",
		RunAs:    "tarantool:tarantool",
		Instances: map[string]string{
			"router-1": "myapp@router-1.service", "storage-1": "myapp@storage-1.service",
			"legacy": "myapp@legacy.service", "gateway": "myapp@gateway.service",
		},
	}, mark)

	assert.Equal(t, map[string]string{
		"myapp:router-1": "myapp@router-1.service", "myapp:storage-1": "myapp@storage-1.service",
		"myapp:legacy": "myapp@legacy.service", "myapp:gateway": "myapp@gateway.service",
	}, managedStatus(t))

	err = internalStopWithConfirmationModule(cmdCtx, []string{"myapp:storage-1"})
	require.ErrorIs(t, err, errManagedBySystemd)
	require.ErrorContains(t, err, "use systemctl stop myapp@storage-1.service")

	// Running again replaces the units, and keeps an instance that runs
	// under its unit.
	pidFile, err := running.PIDFile(root, "myapp", "router-1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(pidFile), 0o755))
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644))

	_, err = captureStdout(t, func() error {
		return internalSystemdInstallModule(cmdCtx, []string{"myapp"})
	})
	require.NoError(t, err)

	// An instance that runs under its unit keeps the units in place.
	err = internalSystemdUninstallModule(cmdCtx, []string{"myapp"})
	require.ErrorIs(t, err, errInstancesRunning)
	require.ErrorContains(t, err, "systemctl disable --now myapp@router-1.service")
	assert.FileExists(t, systemd.MarkPath(hostDir))

	require.NoError(t, os.Remove(pidFile))

	out, err = captureStdout(t, func() error {
		return internalSystemdUninstallModule(cmdCtx, []string{"myapp"})
	})
	require.NoError(t, err)
	assert.Contains(t, out, "systemctl daemon-reload\n")

	entries, err := os.ReadDir(unitDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.NoFileExists(t, systemd.MarkPath(hostDir))
	assert.Empty(t, managedStatus(t))

	// Units that are not installed are nothing to remove, and no failure; a
	// mark that cannot be read is.
	out, err = captureStdout(t, func() error {
		return internalSystemdUninstallModule(cmdCtx, []string{"myapp"})
	})
	require.NoError(t, err)
	assert.Empty(t, out)

	require.NoError(t, os.WriteFile(systemd.MarkPath(hostDir), []byte("units: [\n"), 0o644))

	err = internalSystemdUninstallModule(cmdCtx, []string{"myapp"})
	require.ErrorIs(t, err, systemd.ErrInvalidMark)
	assert.FileExists(t, systemd.MarkPath(hostDir))
}

// TestSystemdRunAs pins --run-as: User= and Group= of the units, the group of
// the user's name without one; install records it with the units and keeps
// it when an install over them names none, and a flag given replaces it.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdRunAs(t *testing.T) {
	root := systemdFixture(t)
	unitDir := t.TempDir()
	hostDir := filepath.Join(root, "etc", "myapp")
	cmdCtx := &cmdcontext.CmdCtx{}

	generate := func(runAs string) string {
		t.Helper()

		setServiceFlags(t, "", "", unitDir)

		if runAs != "" {
			require.NoError(t, generateRunAs.Set(runAs))
		}

		out, err := captureStdout(t, func() error {
			return internalSystemdGenerateModule(cmdCtx, []string{"myapp"})
		})
		require.NoError(t, err)

		return out
	}

	assert.Contains(t, generate(""), "\nUser=tarantool\nGroup=tarantool\n")
	assert.Contains(t, generate("tt-svc:daemon"), "\nUser=tt-svc\nGroup=daemon\n")
	assert.Contains(t, generate("1001"), "\nUser=1001\nGroup=1001\n")

	install := func(runAs string) (string, systemd.Mark) {
		t.Helper()

		setServiceFlags(t, "", "", unitDir)

		if runAs != "" {
			require.NoError(t, installRunAs.Set(runAs))
		}

		_, err := captureStdout(t, func() error {
			return internalSystemdInstallModule(cmdCtx, []string{"myapp"})
		})
		require.NoError(t, err)

		unit, err := os.ReadFile(filepath.Join(unitDir, "myapp@.service"))
		require.NoError(t, err)

		mark, found, err := systemd.ReadMark(hostDir)
		require.NoError(t, err)
		require.True(t, found)

		return string(unit), mark
	}

	unit, mark := install("")
	assert.Contains(t, unit, "\nUser=tarantool\nGroup=tarantool\n")
	assert.Equal(t, "tarantool:tarantool", mark.RunAs)

	unit, mark = install("tt-svc")
	assert.Contains(t, unit, "\nUser=tt-svc\nGroup=tt-svc\n")
	assert.Equal(t, "tt-svc:tt-svc", mark.RunAs)

	// An install without the flag keeps what the units run as.
	unit, mark = install("")
	assert.Contains(t, unit, "\nUser=tt-svc\nGroup=tt-svc\n")
	assert.Equal(t, "tt-svc:tt-svc", mark.RunAs)

	unit, mark = install("tarantool:daemon")
	assert.Contains(t, unit, "\nUser=tarantool\nGroup=daemon\n")
	assert.Equal(t, "tarantool:daemon", mark.RunAs)
}

// TestSystemdInstallRefusesRunning pins that install refuses an instance that
// runs without a unit: once recorded, tt instance stop would refuse it.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdInstallRefusesRunning(t *testing.T) {
	root := systemdFixture(t)
	unitDir := t.TempDir()

	setServiceFlags(t, "", "", unitDir)

	pidFile, err := running.PIDFile(root, "myapp", "gateway")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(pidFile), 0o755))
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644))

	err = internalSystemdInstallModule(&cmdcontext.CmdCtx{}, []string{"myapp"})
	require.ErrorIs(t, err, errInstancesRunning)
	require.ErrorContains(t, err, "tt instance stop myapp:gateway")

	entries, err := os.ReadDir(unitDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.NoFileExists(t, systemd.MarkPath(filepath.Join(root, "etc", "myapp")))

	// The units are installed elsewhere.
	require.NoError(t, os.Remove(pidFile))

	_, err = captureStdout(t, func() error {
		return internalSystemdInstallModule(&cmdcontext.CmdCtx{}, []string{"myapp"})
	})
	require.NoError(t, err)

	setServiceFlags(t, "", "", t.TempDir())

	err = internalSystemdInstallModule(&cmdcontext.CmdCtx{}, []string{"myapp"})
	require.ErrorIs(t, err, errUnitsElsewhere)
}

// TestSystemdAnalyzeVerify checks the units with systemd-analyze verify,
// where the host has it: systemd must take every line tt writes.
//
//nolint:paralleltest // Sets the environment and the flags.
func TestSystemdAnalyzeVerify(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze is not available")
	}

	root := systemdFixture(t)
	dir := t.TempDir()

	setServiceFlags(t, "", dir, systemUnitDir)
	require.NoError(t, internalSystemdGenerateModule(&cmdcontext.CmdCtx{}, []string{"myapp"}))

	for _, inst := range []string{"router-1", "storage-1", "legacy", "gateway"} {
		unit := systemd.UnitName("myapp", inst)

		verify := exec.CommandContext(t.Context(), analyze, "verify", "--man=no",
			filepath.Join(dir, unit))

		verify.Env = append(os.Environ(), "SYSTEMD_LOG_COLOR=0")

		out, err := verify.CombinedOutput()
		require.NoError(t, err, string(out))

		// Complaints about units of the host are not ours.
		for line := range strings.SplitSeq(string(out), "\n") {
			assert.NotContains(t, line, dir, unit)
			assert.NotContains(t, line, "myapp@", unit)
			assert.NotContains(t, line, root, unit)
		}
	}
}
