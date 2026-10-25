package state_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// installVersion lays an installed version of an application the way install
// leaves it: a manifest at the top of its version directory.
func installVersion(t *testing.T, root, name, ver string) string {
	t.Helper()

	dir := state.VersionDir(root, name, ver)
	writeFile(t, filepath.Join(dir, state.PrimaryManifestFile), []byte(manifestTOML(name, nil)))

	return dir
}

// TestScopeRoot pins where each scope keeps its versions/: the project's own
// .tarantool, and the roots tt binary uses for user and system, moved by
// their environment variables. Not parallel: t.Setenv is process-wide.
func TestScopeRoot(t *testing.T) {
	userRoot := t.TempDir()
	systemRoot := t.TempDir()

	t.Setenv("TT_USER_ROOT", userRoot)
	t.Setenv("TT_SYSTEM_ROOT", systemRoot)

	project := t.TempDir()

	root, err := state.ScopeRoot(state.ScopeProject, project)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(project, ".tarantool"), root)

	root, err = state.ScopeRoot(state.ScopeUser, project)
	require.NoError(t, err)
	assert.Equal(t, userRoot, root)

	root, err = state.ScopeRoot(state.ScopeSystem, project)
	require.NoError(t, err)
	assert.Equal(t, systemRoot, root)

	_, err = state.ScopeRoot(state.Scope("global"), project)
	require.ErrorIs(t, err, state.ErrUnknownScope)
}

// TestScopeRootRefusesRelativeRoot pins that a broken root variable is an
// error rather than a fallback to the default root, which would install
// somewhere nobody asked for.
func TestScopeRootRefusesRelativeRoot(t *testing.T) {
	t.Setenv("TT_SYSTEM_ROOT", "relative/root")

	_, err := state.ScopeRoot(state.ScopeSystem, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TT_SYSTEM_ROOT")
}

// TestVersionLayoutPaths fixes the names: versions/<app>/<app>_<ver>, the
// naming of tt binary installations, and apps/<app>.
func TestVersionLayoutPaths(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "my-app_1.2.0", state.VersionName("my-app", "1.2.0"))
	assert.Equal(t, filepath.Join("/root", "versions", "my-app"), state.AppDir("/root", "my-app"))
	assert.Equal(t, filepath.Join("/root", "versions", "my-app", "my-app_1.2.0"),
		state.VersionDir("/root", "my-app", "1.2.0"))
	assert.Equal(t, filepath.Join("/root", "apps", "my-app"), state.ActiveLink("/root", "my-app"))
}

// TestCheckAppName refuses the names of tt binary products, whose directories
// share versions/ with applications.
func TestCheckAppName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"tarantool", "tarantool-ee", "tarantool-sdk", "tt", "tt-ee", "tcm",
	} {
		require.ErrorIs(t, state.CheckAppName(name), state.ErrProductName, name)
	}

	require.NoError(t, state.CheckAppName("my-app"))
	require.NoError(t, state.CheckAppName("tarantool-app"))
}

// TestCheckVersion refuses a version that is not a single path element.
func TestCheckVersion(t *testing.T) {
	t.Parallel()

	for _, ver := range []string{"1.0.0", "1.4.1-dev.3+gabc1234", "2026.10"} {
		require.NoError(t, state.CheckVersion(ver), ver)
	}

	for _, ver := range []string{"", ".", "..", "1.0/../..", `1.0\x`, "a\x00b"} {
		require.ErrorIs(t, state.CheckVersion(ver), state.ErrBadVersion, ver)
	}
}

// TestActivateAndReadApp covers the round trip: installed versions are read
// lowest first, and the link Activate writes selects one of them, relative to
// the root.
func TestActivateAndReadApp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	installVersion(t, root, "my-app", "1.10.0")
	installVersion(t, root, "my-app", "1.2.0")

	require.NoError(t, state.Activate(root, "my-app", "1.2.0"))

	target, err := os.Readlink(state.ActiveLink(root, "my-app"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.2.0"), target)

	app, err := state.ReadApp(root, "my-app")
	require.NoError(t, err)
	assert.Equal(t, []string{"1.2.0", "1.10.0"}, app.Versions, "SemVer order, not string order")
	assert.Equal(t, "1.2.0", app.Active)
	assert.True(t, app.OnDisk)

	require.NoError(t, state.Activate(root, "my-app", "1.10.0"))

	app, err = state.ReadApp(root, "my-app")
	require.NoError(t, err)
	assert.Equal(t, "1.10.0", app.Active)
}

// TestReadAppSkipsDebris: a version directory without a manifest is not an
// installed version, and a link to a version that is not installed selects
// nothing, but both still count as something on disk to remove.
func TestReadAppSkipsDebris(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staging := filepath.Join(state.AppDir(root, "my-app"), ".install-1")

	require.NoError(t, os.MkdirAll(state.VersionDir(root, "my-app", "1.0.0"), 0o750))
	require.NoError(t, os.MkdirAll(staging, 0o750))
	require.NoError(t, state.Activate(root, "my-app", "1.0.0"))

	app, err := state.ReadApp(root, "my-app")
	require.NoError(t, err)
	assert.Empty(t, app.Versions)
	assert.Empty(t, app.Active)
	assert.True(t, app.OnDisk)

	missing, err := state.ReadApp(root, "other")
	require.NoError(t, err)
	assert.Equal(t, state.App{Name: "other"}, missing)
}

// TestReadAppAcceptsAbsoluteLink: a link set by hand to the absolute path of a
// version selects it as well.
func TestReadAppAcceptsAbsoluteLink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := installVersion(t, root, "my-app", "1.0.0")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "apps"), 0o750))
	require.NoError(t, os.Symlink(dir, state.ActiveLink(root, "my-app")))

	app, err := state.ReadApp(root, "my-app")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", app.Active)
}

// TestInstalledApps lists the applications of a root and nothing that only
// shares versions/ with them: the products of tt binary, staging directories
// and directories with no installed version.
func TestInstalledApps(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	installVersion(t, root, "router", "1.0.0")
	installVersion(t, root, "storage", "2.0.0")
	installVersion(t, root, "storage", "2.1.0")
	require.NoError(t, state.Activate(root, "storage", "2.1.0"))

	writeFile(t, filepath.Join(root, "versions", "tarantool", "tarantool_3.8.1", "tarantool"),
		[]byte("#!/bin/sh\n"))
	writeFile(t, filepath.Join(root, "versions", "tt", "tt_3.2.0", state.PrimaryManifestFile),
		[]byte(manifestTOML("tt", nil)))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "versions", ".stage-1"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "versions", "empty"), 0o750))

	apps, err := state.InstalledApps(root)
	require.NoError(t, err)
	require.Len(t, apps, 2)

	assert.Equal(t, "router", apps[0].Name)
	assert.Equal(t, []string{"1.0.0"}, apps[0].Versions)
	assert.Empty(t, apps[0].Active)

	assert.Equal(t, "storage", apps[1].Name)
	assert.Equal(t, []string{"2.0.0", "2.1.0"}, apps[1].Versions)
	assert.Equal(t, "2.1.0", apps[1].Active)

	none, err := state.InstalledApps(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, none)
}

// TestRunningInstances tells running instances by their pid files: one naming
// a live process counts, one naming a process that is gone does not.
func TestRunningInstances(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	running, err := state.RunningInstances(root, "my-app")
	require.NoError(t, err)
	assert.Empty(t, running)

	gone := exec.CommandContext(t.Context(), "true")
	require.NoError(t, gone.Run())

	runDir := filepath.Join(root, "var", "run", "my-app")
	writeFile(t, filepath.Join(runDir, "router-1", "tt.pid"),
		[]byte(strconv.Itoa(os.Getpid())))
	writeFile(t, filepath.Join(runDir, "storage-1", "tt.pid"),
		[]byte(strconv.Itoa(gone.Process.Pid)))
	require.NoError(t, os.MkdirAll(filepath.Join(runDir, "storage-2"), 0o750))

	running, err = state.RunningInstances(root, "my-app")
	require.NoError(t, err)
	assert.Equal(t, []state.RunningInstance{{Name: "router-1", Pid: os.Getpid()}}, running)

	other, err := state.RunningInstances(root, "other")
	require.NoError(t, err)
	assert.Empty(t, other)

	err = state.CheckNotRunning(root, "my-app")
	require.ErrorIs(t, err, state.ErrInstancesRunning)
	assert.Contains(t, err.Error(), "router-1 (pid "+strconv.Itoa(os.Getpid())+")")
	assert.NotContains(t, err.Error(), "storage-1")

	require.NoError(t, state.CheckNotRunning(root, "other"))
}

// TestProjectLayout fixes the layout of a project-shaped install root, which
// a project and an installed version share.
func TestProjectLayout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lay := state.ProjectLayout(dir)

	assert.Equal(t, dir, lay.Root)
	assert.Equal(t, filepath.Join(dir, ".rocks"), lay.Tree)
	assert.Equal(t, filepath.Join(dir, ".rocks", "share", "tarantool"), lay.Share)
	assert.Equal(t, filepath.Join(dir, ".rocks", "lib", "tarantool"), lay.Lib)
	assert.Equal(t, filepath.Join(dir, ".rocks", "manifests"), lay.Manifests)
}

// TestCompareVersions orders SemVer with build metadata, which the LuaRocks
// parser refuses, and puts what does not parse first.
func TestCompareVersions(t *testing.T) {
	t.Parallel()

	assert.Negative(t, state.CompareVersions("1.2.0", "1.10.0"))
	assert.Positive(t, state.CompareVersions("1.4.1", "1.4.1-dev.3+gabc1234"))
	assert.Negative(t, state.CompareVersions("1.4.1-dev.3+gabc1234", "1.4.1-dev.4+gdef5678"))
	assert.Zero(t, state.CompareVersions("2.0.0", "2.0.0"))
	assert.Negative(t, state.CompareVersions("nightly", "0.0.1"))
	assert.Negative(t, state.CompareVersions("a", "b"))
}
