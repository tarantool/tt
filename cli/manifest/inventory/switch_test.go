package inventory_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/inventory"
	"github.com/tarantool/tt/v3/cli/manifest/state"
	"github.com/tarantool/tt/v3/cli/tarantool"
)

// installTarantool lays an installation of tarantool version into versions/
// of root, as tt binary installs it: an executable the application can run on.
func installTarantool(t *testing.T, root, version string) {
	t.Helper()

	path := filepath.Join(root, "versions", "tarantool", "tarantool_"+version, "tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700))
}

// bundleTarantool records version as the tarantool the lock of the installed
// version dir bundles, as a with-deps archive records it.
func bundleTarantool(t *testing.T, dir, version string) {
	t.Helper()

	path := filepath.Join(dir, state.PrimaryLockFile)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	lock, err := manifest.ParseLock(data)
	require.NoError(t, err)

	lock.BundledTarantool = version

	data, err = lock.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// twoVersions installs my-app 1.0.0 and 2.0.0 into root, both bundling
// tarantool 3.8.1, which is installed, with 2.0.0 active, as an upgrade
// leaves them.
func twoVersions(t *testing.T, root string) {
	t.Helper()

	installTarantool(t, root, "3.8.1")

	for _, version := range []string{"1.0.0", "2.0.0"} {
		bundleTarantool(t, installVersion(t, root, pkg{name: "my-app", version: version}), "3.8.1")
	}

	require.NoError(t, state.Activate(root, "my-app", "2.0.0"))
}

// activeTarget returns what the active link of my-app points at.
func activeTarget(t *testing.T, root string) string {
	t.Helper()

	target, err := os.Readlink(filepath.Join(root, "apps", "my-app"))
	require.NoError(t, err)

	return target
}

// TestSwitchUserScope points the active link at the version asked for, in the
// relative form install writes, and leaves every version in place: a rollback
// is a link, not a reinstall.
func TestSwitchUserScope(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)

	var warnings []string

	result, err := inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "1.0.0",
		Warn: func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, &inventory.SwitchResult{
		Package: "my-app", Scope: state.ScopeUser, Version: "1.0.0", Previous: "2.0.0",
		AlreadyActive: false, Running: nil,
	}, result)
	assert.Empty(t, warnings)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.0.0"),
		activeTarget(t, root))

	app, err := state.ReadApp(root, "my-app")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", app.Active)
	assert.Equal(t, []string{"1.0.0", "2.0.0"}, app.Versions)

	// The link is replaced by a rename: nothing of the replacement is left
	// next to it.
	entries, err := os.ReadDir(filepath.Join(root, "apps"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "my-app", entries[0].Name())

	// And forward again.
	result, err = inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "2.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", result.Previous)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_2.0.0"),
		activeTarget(t, root))
}

// TestSwitchSystemScope: the system scope works the same, under its own root.
func TestSwitchSystemScope(t *testing.T) {
	appRoots(t)

	root := os.Getenv("TT_SYSTEM_ROOT")
	twoVersions(t, root)

	_, err := inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeSystem, Package: "my-app", Version: "1.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.0.0"),
		activeTarget(t, root))
}

// TestSwitchWithNoActiveVersion: an application whose link is gone, or
// points at no installed version, gets one.
func TestSwitchWithNoActiveVersion(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)
	require.NoError(t, os.Remove(filepath.Join(root, "apps", "my-app")))

	result, err := inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "1.0.0",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Previous)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.0.0"),
		activeTarget(t, root))
}

// TestSwitchAlreadyActive does nothing, and says so, for the active version.
func TestSwitchAlreadyActive(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)

	link := filepath.Join(root, "apps", "my-app")
	before, err := os.Lstat(link)
	require.NoError(t, err)

	result, err := inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "2.0.0",
	})
	require.NoError(t, err)
	assert.True(t, result.AlreadyActive)
	assert.Equal(t, "2.0.0", result.Version)

	after, err := os.Lstat(link)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "the link is not replaced")
}

// TestSwitchRefusesUnknownVersion names the installed versions, the active
// one marked, and leaves the link alone. A version directory an interrupted
// install left without a manifest is no installed version.
func TestSwitchRefusesUnknownVersion(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)
	require.NoError(t, os.MkdirAll(state.VersionDir(root, "my-app", "3.0.0"), 0o750))

	for _, version := range []string{"9.9.9", "3.0.0"} {
		_, err := inventory.Switch(inventory.SwitchOptions{
			Scope: state.ScopeUser, Package: "my-app", Version: version,
		})
		require.ErrorIs(t, err, inventory.ErrVersionNotInstalled)
		assert.Equal(t, 1, exitcode.Code(err))
		assert.Contains(t, err.Error(), "my-app "+version+" in user scope")
		assert.Contains(t, err.Error(), "installed versions: 1.0.0, 2.0.0 (active)")
	}

	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_2.0.0"),
		activeTarget(t, root))
}

// TestSwitchRefusesBadArguments: a version that cannot name a directory, a
// name that is no package or is that of a tt binary product, and an
// application that is not installed, are refused before anything is touched.
func TestSwitchRefusesBadArguments(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)

	cases := []struct {
		name, pkg, version string
		want               error
	}{
		{"traversal version", "my-app", "../../tarantool/tarantool_3.8.1", state.ErrBadVersion},
		{"empty version", "my-app", "", state.ErrBadVersion},
		{"bad name", "../my-app", "1.0.0", inventory.ErrBadPackageName},
		{"product", "tarantool", "3.8.1", state.ErrProductName},
		{"not installed", "other", "1.0.0", inventory.ErrNotInstalled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inventory.Switch(inventory.SwitchOptions{
				Scope: state.ScopeUser, Package: tc.pkg, Version: tc.version,
			})
			require.ErrorIs(t, err, tc.want)
			assert.Equal(t, 1, exitcode.Code(err))
		})
	}

	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_2.0.0"),
		activeTarget(t, root))
}

// TestSwitchRefusesProjectScope: a project holds one version of every
// package, and the refusal points at what replaces it there.
func TestSwitchRefusesProjectScope(t *testing.T) {
	appRoots(t)

	for _, scope := range []state.Scope{state.ScopeProject, ""} {
		_, err := inventory.Switch(inventory.SwitchOptions{
			ProjectDir: t.TempDir(), Scope: scope, Package: "my-app", Version: "1.0.0",
		})
		require.ErrorIs(t, err, inventory.ErrSwitchScope)
		assert.Equal(t, 1, exitcode.Code(err))
		assert.Contains(t, err.Error(), "tt package install --force")
	}

	_, err := inventory.InstalledApp(inventory.SwitchOptions{
		ProjectDir: t.TempDir(), Scope: state.ScopeProject, Package: "my-app",
	})
	require.ErrorIs(t, err, inventory.ErrSwitchScope)
}

// TestSwitchRefusesVersionWithoutRuntime: a version whose bundled tarantool
// has been removed from the scope could not start, so it is not made active,
// and the error tells what to install.
func TestSwitchRefusesVersionWithoutRuntime(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)
	bundleTarantool(t, state.VersionDir(root, "my-app", "1.0.0"), "3.7.0")

	_, err := inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "1.0.0",
	})
	require.ErrorIs(t, err, inventory.ErrVersionCannotStart)
	require.ErrorIs(t, err, tarantool.ErrNotInstalled)
	assert.Equal(t, 1, exitcode.Code(err))
	assert.Contains(t, err.Error(), "cannot switch my-app to 1.0.0: the version could not start")
	assert.Contains(t, err.Error(), "tt binary install tarantool 3.7.0 --scope user")
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_2.0.0"),
		activeTarget(t, root))

	installTarantool(t, root, "3.7.0")

	_, err = inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "1.0.0",
	})
	require.NoError(t, err)
}

// TestSwitchWarnsVersionWithoutDeps: a version packed --without-deps bundles
// no tarantool, and one satisfying its [platform].tarantool missing is only a
// warning: that is up to tt binary, and the active version is in the same
// state.
func TestSwitchWarnsVersionWithoutDeps(t *testing.T) {
	root := appRoots(t)

	for _, version := range []string{"1.0.0", "2.0.0"} {
		installVersion(t, root, pkg{name: "my-app", version: version})
	}

	require.NoError(t, state.Activate(root, "my-app", "2.0.0"))

	var warnings []string

	_, err := inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "1.0.0",
		Warn: func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "my-app 1.0.0: tarantool is not installed")
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.0.0"),
		activeTarget(t, root))
}

// TestSwitchReportsRunningInstances: the instances that run keep running and
// are reported, each with the version its watchdog recorded it runs, if any,
// for the caller to tell which to restart.
func TestSwitchReportsRunningInstances(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)

	for _, inst := range []string{"router-1", "storage-1"} {
		holdPidFile(t, filepath.Join(root, "var", "run", "my-app", inst, "tt.pid"), os.Getpid())
	}

	// The watchdog of router-1 recorded the version it started it from.
	writeAt(t, filepath.Join(root, "var", "run", "my-app", "router-1", "app.dir"),
		state.VersionDir(root, "my-app", "2.0.0")+"\n")

	// A pid file nobody holds is no running instance.
	writeAt(t, filepath.Join(root, "var", "run", "my-app", "stale", "tt.pid"), "1")

	result, err := inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "1.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, []inventory.RunningInstance{
		{
			Name: "router-1", Pid: os.Getpid(), Version: "2.0.0",
			Dir: state.VersionDir(root, "my-app", "2.0.0"),
		},
		{Name: "storage-1", Pid: os.Getpid(), Version: "", Dir: ""},
	}, result.Running)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.0.0"),
		activeTarget(t, root))

	// Already active, the instances are still told: they may not run it yet.
	result, err = inventory.Switch(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app", Version: "1.0.0",
	})
	require.NoError(t, err)
	assert.True(t, result.AlreadyActive)
	assert.Len(t, result.Running, 2)

	running, err := inventory.RunningInstances(state.ScopeUser, "", "my-app")
	require.NoError(t, err)
	assert.Equal(t, result.Running, running)
}

// TestInstalledApp lists the installed versions with the active one, for the
// choice switch offers without a version.
func TestInstalledApp(t *testing.T) {
	root := appRoots(t)
	twoVersions(t, root)

	app, err := inventory.InstalledApp(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "my-app",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"1.0.0", "2.0.0"}, app.Versions)
	assert.Equal(t, "2.0.0", app.Active)
	assert.Equal(t, "1.0.0, 2.0.0 (active)", inventory.DescribeVersions(app))

	_, err = inventory.InstalledApp(inventory.SwitchOptions{
		Scope: state.ScopeUser, Package: "other",
	})
	require.ErrorIs(t, err, inventory.ErrNotInstalled)
}
