package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// The tests here install into the user and system scopes, whose roots come
// from TT_USER_ROOT and TT_SYSTEM_ROOT; they point both at temp directories,
// so nothing reaches the real roots, and they cannot run in parallel.

// userRoot points the user scope at a fresh temp directory and returns it.
func userRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	t.Setenv("TT_USER_ROOT", root)
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	return root
}

// systemRoot points the system scope at a fresh temp directory and returns it.
func systemRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	t.Setenv("TT_SYSTEM_ROOT", root)
	t.Setenv("TT_USER_ROOT", t.TempDir())

	return root
}

// appSpec is a --without-deps archive of an application that carries its own
// files only, so installing it fetches nothing.
func appSpec(name, version string) archiveSpec {
	return archiveSpec{name: name, version: version, files: rockFiles(name, version)}
}

// myAppV1With is appSpec of my-app 1.0.0 with extra archive entries.
func myAppV1With(extra map[string]string) archiveSpec {
	return archiveSpec{
		name: "my-app", version: "1.0.0",
		files: mergeFiles(rockFiles("my-app", "1.0.0"), extra),
	}
}

// activeTarget reads apps/my-app of a root.
func activeTarget(t *testing.T, root string) string {
	t.Helper()

	target, err := os.Readlink(filepath.Join(root, "apps", "my-app"))
	require.NoError(t, err)

	return target
}

// versionPath is versions/my-app/my-app_<ver> of a root, spelled out rather
// than taken from state so the layout itself is what the tests pin.
func versionPath(root, ver string) string {
	return filepath.Join(root, "versions", "my-app", "my-app_"+ver)
}

// leftovers lists the entries of versions/<name>/ that are not installed
// versions: the staging and replaced directories an install must not leave.
func leftovers(t *testing.T, root, name string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(root, "versions", name))
	require.NoError(t, err)

	var names []string

	for _, entry := range entries {
		if entry.Name()[0] == '.' {
			names = append(names, entry.Name())
		}
	}

	return names
}

// TestInstallVersionsSideBySide is the headline case of decision 8: two
// versions of an application installed into the user scope live side by side
// as project-shaped install roots, and apps/<app> selects the one installed
// last.
func TestInstallVersionsSideBySide(t *testing.T) {
	root := userRoot(t)

	result, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser},
		appSpec("my-app", "1.0.0").build(t))
	require.NoError(t, err)
	require.Len(t, result.Installed, 1)
	assert.Equal(t, ScopeUser, result.Installed[0].Scope)

	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser},
		appSpec("my-app", "2.0.0").build(t))
	require.NoError(t, err)

	for _, ver := range []string{"1.0.0", "2.0.0"} {
		dir := versionPath(root, ver)

		assert.FileExists(t, filepath.Join(dir, "app.manifest.toml"))
		assert.FileExists(t, filepath.Join(dir, "app.manifest.lock"))
		assert.Equal(t, ver+"\n", readFile(t, filepath.Join(dir, "VERSION")))
		assert.FileExists(t,
			filepath.Join(dir, ".rocks", "share", "tarantool", "my-app", "init.lua"))
		assert.NoDirExists(t, filepath.Join(dir, ".rocks", "manifests"),
			"a version is its own primary install root, with no guest metadata")
	}

	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_2.0.0"),
		activeTarget(t, root))
	assert.Empty(t, leftovers(t, root, "my-app"))

	// Install activates what it installed, lower or not.
	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser},
		appSpec("my-app", "1.5.0").build(t))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.5.0"),
		activeTarget(t, root))

	// Nothing of the LuaRocks layout this scope used to have.
	assert.NoDirExists(t, filepath.Join(root, "share"))
	assert.NoDirExists(t, filepath.Join(root, "manifests"))
}

// TestInstallVersionLaysArchiveWhole: the version directory receives every
// member of the archive, payload and license files included, since an
// instance description resolves ./bin/<tool> from the application directory.
func TestInstallVersionLaysArchiveWhole(t *testing.T) {
	root := userRoot(t)

	spec := myAppV1With(map[string]string{
		"README.md":   "my-app\n",
		"LICENSE":     "MIT\n",
		"bin/gateway": "#!/bin/sh\n",
		// tt's own install state never comes from an archive.
		".rocks/manifests/other/manifest.toml": "junk\n",
	})

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser}, spec.build(t))
	require.NoError(t, err)

	dir := versionPath(root, "1.0.0")
	assert.Equal(t, "my-app\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.Equal(t, "MIT\n", readFile(t, filepath.Join(dir, "LICENSE")))
	assert.FileExists(t, filepath.Join(dir, "bin", "gateway"))
	assert.NoDirExists(t, filepath.Join(dir, ".rocks", "manifests"))
}

// TestInstallVersionCollision: the same version again is exit 1, and --force
// replaces it whole, leaving nothing of the old one and no temporary
// directory behind.
func TestInstallVersionCollision(t *testing.T) {
	root := userRoot(t)

	old := myAppV1With(map[string]string{"stale.lua": "-- old\n"})

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser}, old.build(t))
	require.NoError(t, err)

	archive := myAppV1With(map[string]string{"fresh.lua": "-- new\n"}).build(t)

	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser}, archive)
	require.ErrorIs(t, err, errNameCollision)
	assert.Equal(t, 1, exitcode.Code(err))
	assert.Contains(t, err.Error(), "--force")

	dir := versionPath(root, "1.0.0")
	assert.FileExists(t, filepath.Join(dir, "stale.lua"), "a refused install changes nothing")

	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser, Force: true}, archive)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "fresh.lua"))
	assert.NoFileExists(t, filepath.Join(dir, "stale.lua"), "--force replaces the version whole")
	assert.Empty(t, leftovers(t, root, "my-app"))
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.0.0"),
		activeTarget(t, root))
}

// TestInstallVersionForceRefusedWhileRunning: --force does not replace a
// version while an instance of the application runs, as its pid file under
// var/run/<app>/ says.
func TestInstallVersionForceRefusedWhileRunning(t *testing.T) {
	root := userRoot(t)

	old := myAppV1With(map[string]string{"stale.lua": "-- old\n"})

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser}, old.build(t))
	require.NoError(t, err)

	pidFile := filepath.Join(root, "var", "run", "my-app", "router-1", "tt.pid")
	require.NoError(t, os.MkdirAll(filepath.Dir(pidFile), dirPerm))
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), filePerm))

	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser, Force: true},
		appSpec("my-app", "1.0.0").build(t))
	require.ErrorIs(t, err, state.ErrInstancesRunning)
	assert.Equal(t, 1, exitcode.Code(err))
	assert.Contains(t, err.Error(), "router-1")
	assert.FileExists(t, filepath.Join(versionPath(root, "1.0.0"), "stale.lua"))

	// A new version is not a replacement: it goes in next to the running one.
	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser},
		appSpec("my-app", "1.1.0").build(t))
	require.NoError(t, err)
}

// TestInstallVersionUpgrade: --upgrade installs only a version higher than
// every installed one.
func TestInstallVersionUpgrade(t *testing.T) {
	root := userRoot(t)

	upgrade := Options{Scope: ScopeUser, Upgrade: true}

	result, err := installInto(t, t.TempDir(), upgrade, appSpec("my-app", "1.0.0").build(t))
	require.NoError(t, err)
	assert.False(t, result.Installed[0].Skipped, "nothing installed yet: --upgrade installs")

	for _, ver := range []string{"1.0.0", "0.9.0"} {
		result, err = installInto(t, t.TempDir(), upgrade, appSpec("my-app", ver).build(t))
		require.NoError(t, err)
		assert.True(t, result.Installed[0].Skipped, ver)
	}

	assert.NoDirExists(t, versionPath(root, "0.9.0"))

	result, err = installInto(t, t.TempDir(), upgrade, appSpec("my-app", "2.0.0").build(t))
	require.NoError(t, err)
	assert.False(t, result.Installed[0].Skipped)
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_2.0.0"),
		activeTarget(t, root))
}

// TestInstallVersionRefusesProductName: an application named like a product of
// tt binary would share its directory in versions/, so the user and system
// scopes refuse it before writing anything.
func TestInstallVersionRefusesProductName(t *testing.T) {
	root := userRoot(t)

	for _, scope := range []Scope{ScopeUser, ScopeSystem} {
		for _, name := range []string{"tarantool", "tt", "tcm", "tarantool-ee"} {
			_, err := installInto(t, t.TempDir(), Options{Scope: scope},
				appSpec(name, "1.0.0").build(t))
			require.ErrorIs(t, err, state.ErrProductName, name)
			assert.Equal(t, 1, exitcode.Code(err))
		}
	}

	assert.NoDirExists(t, filepath.Join(root, "versions"))
}

// TestInstallVersionRefusesBadVersion: a version that cannot name a directory
// is refused before anything is written.
func TestInstallVersionRefusesBadVersion(t *testing.T) {
	root := userRoot(t)

	for _, ver := range []string{"", "../../etc"} {
		_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser},
			appSpec("my-app", ver).build(t))
		require.ErrorIs(t, err, state.ErrBadVersion, ver)
		assert.Equal(t, 1, exitcode.Code(err))
	}

	assert.NoDirExists(t, filepath.Join(root, "versions"))
}

// TestInstallSystemScope: the system scope installs under TT_SYSTEM_ROOT in
// the same layout, and ignores the project directory it is handed.
func TestInstallSystemScope(t *testing.T) {
	root := systemRoot(t)
	project := t.TempDir()

	_, err := installInto(t, project, Options{Scope: ScopeSystem},
		appSpec("my-app", "1.0.0").build(t))
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(versionPath(root, "1.0.0"), "app.manifest.toml"))
	assert.Equal(t, filepath.Join("..", "versions", "my-app", "my-app_1.0.0"),
		activeTarget(t, root))

	entries, err := os.ReadDir(project)
	require.NoError(t, err)
	assert.Empty(t, entries, "the project directory is not touched")
}

// TestInstallVersionReadableByOthers: the version directory gets the mode of
// the directories of an installation, not the 0700 of the temporary directory
// it is staged in, and so does everything in it: in the system scope the
// instances run as the user of the systemd units, not as the one that
// installs.
func TestInstallVersionReadableByOthers(t *testing.T) {
	root := systemRoot(t)

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeSystem},
		myAppV1With(map[string]string{"bin/gateway": "#!/bin/sh\n"}).build(t))
	require.NoError(t, err)

	dir := versionPath(root, "1.0.0")

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, dirPerm, info.Mode().Perm())

	// The directories in the version are made with dirPerm through the
	// umask, as versions/my-app/ next to it is.
	parent, err := os.Stat(filepath.Dir(dir))
	require.NoError(t, err)

	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == dir || !entry.IsDir() {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		assert.Equal(t, parent.Mode().Perm(), info.Mode().Perm(), path)

		return nil
	})
	require.NoError(t, err)
}

// TestInstallVersionRollsBackFailedFetch: when a dependency cannot be fetched,
// the new version is removed and the one --force was replacing is restored.
func TestInstallVersionRollsBackFailedFetch(t *testing.T) {
	root := userRoot(t)

	old := myAppV1With(map[string]string{"stale.lua": "-- old\n"})

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser}, old.build(t))
	require.NoError(t, err)

	// The archive locks a registry rock it does not carry, and no server holds
	// it, so the fetch fails without touching a network.
	broken := func(name string) string {
		return archiveSpec{
			name: name, version: "1.0.0", files: rockFiles(name, "1.0.0"),
			deps:     map[string]string{sharedDep: ">=3.0.0"},
			lockDeps: []LockDep{{Name: sharedDep, Version: "3.0.4", Source: "registry"}},
		}.build(t)
	}
	servers := []string{filepath.Join(t.TempDir(), "no-such-server")}

	_, err = installInto(t, t.TempDir(),
		Options{Scope: ScopeUser, Force: true, Servers: servers}, broken("my-app"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "installing luasocket 3.0.4", "the fetch is what failed")

	assert.FileExists(t, filepath.Join(versionPath(root, "1.0.0"), "stale.lua"),
		"the replaced version is back")
	assert.Empty(t, leftovers(t, root, "my-app"))

	// A first install that fails leaves nothing behind.
	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser, Servers: servers},
		broken("fresh-app"))
	require.Error(t, err)
	assert.NoDirExists(t, filepath.Join(root, "versions", "fresh-app"))
	assert.NoFileExists(t, filepath.Join(root, "apps", "fresh-app"))
}
