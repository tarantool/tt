package inventory_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/inventory"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// The user and system scopes hold applications, every version installed into
// <root>/versions/<app>/<app>_<ver>/ and the active one selected by
// <root>/apps/<app>. These tests point TT_USER_ROOT and TT_SYSTEM_ROOT at temp
// directories, so the real roots are never read or written, and cannot run in
// parallel: t.Setenv is process-wide.

// appRoots points both roots at fresh temp directories and returns the user
// root.
func appRoots(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	t.Setenv("TT_USER_ROOT", root)
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	return root
}

// installVersion lays one installed version of an application the way install
// leaves it: a project-shaped install root whose primary package is the
// application.
func installVersion(t *testing.T, root string, spec pkg) string {
	t.Helper()

	dir := state.VersionDir(root, spec.name, spec.version)
	version := tree{dir: dir, lay: state.ProjectLayout(dir)}
	version.installPrimary(t, spec)

	return dir
}

// writeAt creates a file and its parents.
func writeAt(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// TestListUserScope lists applications from versions/ and apps/: one entry per
// application, with the active version, every installed one and the
// description of the active one, and nothing for the products of tt binary
// that share versions/.
func TestListUserScope(t *testing.T) {
	root := appRoots(t)

	installVersion(t, root, pkg{name: "my-app", version: "1.0.0", description: "old"})
	installVersion(t, root, pkg{
		name: "my-app", version: "1.2.0", description: "current",
		pins: map[string]string{"metrics": "1.0.0"},
	})
	installVersion(t, root, pkg{name: "my-app", version: "2.0.0", description: "newest"})
	require.NoError(t, state.Activate(root, "my-app", "1.2.0"))

	installVersion(t, root, pkg{name: "other", version: "0.1.0", description: "inactive"})

	writeAt(t, filepath.Join(root, "versions", "tarantool", "tarantool_3.8.1", "tarantool"),
		"#!/bin/sh\n")

	listing, err := inventory.List(inventory.ListOptions{
		Scope: state.ScopeUser, ProjectDir: t.TempDir(),
	})
	require.NoError(t, err)

	assert.Equal(t, state.ScopeUser, listing.Scope)
	assert.Equal(t, root, listing.Root)
	require.Len(t, listing.Packages, 2)

	app := listing.Packages[0]
	assert.Equal(t, "my-app", app.Name)
	assert.Equal(t, "1.2.0", app.Version)
	assert.Equal(t, []string{"1.0.0", "1.2.0", "2.0.0"}, app.Versions)
	assert.Equal(t, "current", app.Description)
	assert.Equal(t, map[string]string{"metrics": "1.0.0"}, app.Dependencies)
	assert.False(t, app.Primary)

	other := listing.Packages[1]
	assert.Equal(t, "other", other.Name)
	assert.Empty(t, other.Version, "no version is active")
	assert.Equal(t, "inactive", other.Description, "described by its highest version")

	var out bytes.Buffer

	require.NoError(t, listing.Human(&out))
	assert.Contains(t, out.String(), "ACTIVE")
	assert.Contains(t, out.String(), "1.0.0, 1.2.0, 2.0.0")
	assert.NotContains(t, out.String(), "tarantool")
}

// TestListSystemScopeEmpty: a root with nothing installed is an empty listing
// naming the root, not an error.
func TestListSystemScopeEmpty(t *testing.T) {
	appRoots(t)

	listing, err := inventory.List(inventory.ListOptions{Scope: state.ScopeSystem})
	require.NoError(t, err)
	assert.Empty(t, listing.Packages)

	var out bytes.Buffer

	require.NoError(t, listing.Human(&out))
	assert.Contains(t, out.String(), "no packages installed in system scope")
}

// TestListUserScopeRefusesTree: the rock index answers what packages of one
// tree share, and the versions of the user scope share no tree.
func TestListUserScopeRefusesTree(t *testing.T) {
	appRoots(t)

	_, err := inventory.List(inventory.ListOptions{Scope: state.ScopeUser, Rocks: true})
	require.ErrorIs(t, err, inventory.ErrTreeScope)
	assert.Equal(t, 1, exitcode.Code(err))
}

// TestUninstallUserScope removes the application with all its versions and
// its active link, and nothing else: the runtimes in versions/ other
// applications may use, the instance data under var/ and the host files
// under etc/ stay (decision 16).
func TestUninstallUserScope(t *testing.T) {
	root := appRoots(t)

	installVersion(t, root, pkg{name: "my-app", version: "1.0.0"})
	installVersion(t, root, pkg{name: "my-app", version: "2.0.0"})
	require.NoError(t, state.Activate(root, "my-app", "2.0.0"))
	installVersion(t, root, pkg{name: "other", version: "1.0.0"})

	kept := []string{
		filepath.Join(root, "versions", "tarantool", "tarantool_3.8.1", "tarantool"),
		filepath.Join(root, "var", "lib", "my-app", "router-1", "00000.snap"),
		filepath.Join(root, "var", "log", "my-app", "router-1", "tt.log"),
		filepath.Join(root, "etc", "my-app", "instances.yaml"),
		filepath.Join(root, "etc", "my-app", "config.yaml"),
	}
	for _, path := range kept {
		writeAt(t, path, "kept\n")
	}

	// A pid file naming a process that is gone does not block the removal.
	writeAt(t, filepath.Join(root, "var", "run", "my-app", "router-1", "tt.pid"), "0")

	var prompts []string

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "my-app",
		Confirm: func(prompt string) bool {
			prompts = append(prompts, prompt)

			return true
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "my-app", result.Package)
	assert.Equal(t, "2.0.0", result.Version)
	assert.Equal(t, []string{"1.0.0", "2.0.0"}, result.Versions)
	assert.Equal(t, state.ScopeUser, result.Scope)
	assert.Equal(t, []string{"remove my-app with its installed versions 1.0.0, 2.0.0?"}, prompts)

	assert.NoDirExists(t, filepath.Join(root, "versions", "my-app"))
	assert.NoFileExists(t, filepath.Join(root, "apps", "my-app"))
	assert.DirExists(t, filepath.Join(root, "versions", "other"))

	for _, path := range kept {
		assert.FileExists(t, path)
	}
}

// TestUninstallUserScopeRefusesRunning: an application whose instance runs is
// not removed, and the error names the instance.
func TestUninstallUserScopeRefusesRunning(t *testing.T) {
	root := appRoots(t)

	dir := installVersion(t, root, pkg{name: "my-app", version: "1.0.0"})
	require.NoError(t, state.Activate(root, "my-app", "1.0.0"))

	writeAt(t, filepath.Join(root, "var", "run", "my-app", "router-1", "tt.pid"),
		strconv.Itoa(os.Getpid()))

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "my-app", Yes: true,
	})
	require.ErrorIs(t, err, state.ErrInstancesRunning)
	assert.Equal(t, 1, exitcode.Code(err))
	assert.Contains(t, err.Error(), "router-1")

	assert.DirExists(t, dir)
	assert.FileExists(t, filepath.Join(root, "apps", "my-app"))
}

// TestUninstallUserScopeAbortKeepsEverything: declining the prompt removes
// nothing.
func TestUninstallUserScopeAbortKeepsEverything(t *testing.T) {
	root := appRoots(t)

	dir := installVersion(t, root, pkg{name: "my-app", version: "1.0.0"})

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "my-app",
		Confirm: func(string) bool { return false },
	})
	require.ErrorIs(t, err, inventory.ErrAborted)
	assert.DirExists(t, dir)
}

// TestUninstallUserScopeRefusesWhatIsNoApplication: a name nothing is installed
// under, and the name of a tt binary product, are refused before anything is
// removed.
func TestUninstallUserScopeRefusesWhatIsNoApplication(t *testing.T) {
	root := appRoots(t)

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "my-app", Yes: true,
	})
	require.ErrorIs(t, err, inventory.ErrNotInstalled)

	installation := filepath.Join(root, "versions", "tarantool", "tarantool_3.8.1", "tarantool")
	writeAt(t, installation, "#!/bin/sh\n")

	_, err = inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeSystem, Package: "tarantool", Yes: true,
	})
	require.ErrorIs(t, err, state.ErrProductName)
	assert.Contains(t, err.Error(), "tt binary uninstall")

	_, err = inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "tarantool", Yes: true,
	})
	require.ErrorIs(t, err, state.ErrProductName)
	assert.FileExists(t, installation)
}

// TestUninstallUserScopeFinishesInterruptedRemoval: what an interrupted
// removal leaves — a version directory without a manifest, or a link alone —
// is still removed by a second run.
func TestUninstallUserScopeFinishesInterruptedRemoval(t *testing.T) {
	root := appRoots(t)

	require.NoError(t, os.MkdirAll(state.VersionDir(root, "my-app", "1.0.0"), 0o750))

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "my-app", Yes: true,
	})
	require.NoError(t, err)
	assert.Empty(t, result.Versions)
	assert.NoDirExists(t, filepath.Join(root, "versions", "my-app"))

	require.NoError(t, state.Activate(root, "my-app", "1.0.0"))

	_, err = inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "my-app", Yes: true,
	})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(root, "apps", "my-app"))
}

// TestUninstallUserScopeIgnoresProjectDir pins that the user scope is addressed
// by scope alone: a ProjectDir pointing at an unrelated tree must not redirect
// the removal.
func TestUninstallUserScopeIgnoresProjectDir(t *testing.T) {
	root := appRoots(t)

	installVersion(t, root, pkg{name: "some-lib", version: "2.0.0"})

	// A project tree holding a same-named guest, which must be left alone.
	project := newTree(t)
	project.installGuest(t, pkg{name: "some-lib", version: "2.0.0"})

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: project.dir, Scope: state.ScopeUser, Package: "some-lib", Yes: true,
	})
	require.NoError(t, err)

	assert.NoDirExists(t, filepath.Join(root, "versions", "some-lib"))
	assert.True(t, project.metadataExists("some-lib"), "the project tree is untouched")
}
