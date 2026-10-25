package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

// fullVersion is a bundled tarantool version as pack records it: the full
// version the binary reports, longer than what tt binary install takes.
const fullVersion = "3.8.1-0-ge643f88a5e0c"

// withRuntimeSpec is a with-deps archive of an application bundling tarantool
// fullVersion and tt 3.2.0.
func withRuntimeSpec(name, version string) archiveSpec {
	return archiveSpec{
		name: name, version: version, withRuntime: fullVersion,
		files: rockFiles(name, version),
	}
}

// warnings collects what an install warns about.
type warnings []string

func (w *warnings) add(msg string) { *w = append(*w, msg) }

// tarantoolInstallation is versions/tarantool/tarantool_<fullVersion> of root.
func tarantoolInstallation(root string) string {
	return filepath.Join(root, "versions", "tarantool", "tarantool_"+fullVersion)
}

// TestInstallUserLaysRuntimeOnce is decision 8 for the bundled runtime: a
// with-deps archive is accepted in the user scope, its _runtime/ is installed
// into versions/ the way tt binary installs the products — once for two
// versions that bundle the same runtime — and the resolver finds the
// tarantool of each version there by its lock. The active links of the scope
// are not touched.
func TestInstallUserLaysRuntimeOnce(t *testing.T) {
	root := userRoot(t)

	// The scope already selects a tarantool for the shell.
	shellTarantool := filepath.Join(t.TempDir(), "tarantool")
	require.NoError(t, os.WriteFile(shellTarantool, []byte("#!/bin/sh\n"), execPerm))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), dirPerm))
	require.NoError(t, os.Symlink(shellTarantool, filepath.Join(root, "bin", "tarantool")))

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser},
		withRuntimeSpec("my-app", "1.0.0").build(t))
	require.NoError(t, err)

	installation := tarantoolInstallation(root)
	assert.Equal(t, "#!/bin/sh\necho tarantool\n",
		readFile(t, filepath.Join(installation, "tarantool")))
	assert.FileExists(t, filepath.Join(installation, "include", "tarantool", "module.h"))
	assert.FileExists(t, filepath.Join(root, "versions", "tt", "tt_3.2.0", "tt"))

	// The shape tt binary installs, and nothing else of _runtime/.
	entries, err := os.ReadDir(installation)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	assert.ElementsMatch(t, []string{"tarantool", "include"}, names)
	require.NoError(t, binary.ValidateInstallation(root,
		binary.Selection{Program: binary.ProgramCe, ID: fullVersion}),
		"tt binary takes it for a complete installation")

	listed, err := binary.InstalledVersions(root, binary.ProgramCe)
	require.NoError(t, err)
	assert.Equal(t, []string{fullVersion}, listed)

	// A second version bundling the same runtime finds it in place and leaves
	// it alone.
	marker := []byte("#!/bin/sh\necho already here\n")
	require.NoError(t, os.WriteFile(filepath.Join(installation, "tarantool"), marker, execPerm))

	_, err = installInto(t, t.TempDir(), Options{Scope: ScopeUser},
		withRuntimeSpec("my-app", "2.0.0").build(t))
	require.NoError(t, err)
	assert.Equal(t, string(marker), readFile(t, filepath.Join(installation, "tarantool")))

	// The versions carry no _runtime/ of their own and run on the shared one.
	for _, ver := range []string{"1.0.0", "2.0.0"} {
		assert.NoDirExists(t, filepath.Join(versionPath(root, ver), "_runtime"))

		exe, err := tarantool.Resolve(tarantool.App{
			Scope: binary.ScopeUser, Dir: versionPath(root, ver),
		})
		require.NoError(t, err, ver)
		assert.Equal(t, filepath.Join(installation, "tarantool"), exe.Path)
		assert.Equal(t, tarantool.OriginLock, exe.Origin)
	}

	// bin/ of the scope still selects the shell's tarantool, and nothing was
	// added to include/.
	target, err := os.Readlink(filepath.Join(root, "bin", "tarantool"))
	require.NoError(t, err)
	assert.Equal(t, shellTarantool, target)
	assert.NoDirExists(t, filepath.Join(root, "include"))
	assert.NoFileExists(t, filepath.Join(root, "bin", "tt"))
}

// TestInstallSystemLaysRuntime: the system scope installs the runtime into its
// own root.
func TestInstallSystemLaysRuntime(t *testing.T) {
	root := systemRoot(t)

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeSystem},
		withRuntimeSpec("my-app", "1.0.0").build(t))
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(tarantoolInstallation(root), "tarantool"))

	exe, err := tarantool.Resolve(tarantool.App{
		Scope: binary.ScopeSystem, Dir: versionPath(root, "1.0.0"),
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tarantoolInstallation(root), "tarantool"), exe.Path)
}

// TestInstallEnterpriseRuntime: the lock records the version alone, so the
// edition comes from the [platform] flavor: an [ee] application's runtime is
// installed as tarantool-ee, from the flat layout of the SDK, and tcm next to
// it.
func TestInstallEnterpriseRuntime(t *testing.T) {
	root := userRoot(t)

	spec := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: fullVersion,
		tarantoolConstraint: ">=3.0.0[ee]",
		files:               rockFiles("my-app", "1.0.0"),
		runtime: map[string]string{
			"_runtime/tarantool/tarantool": "#!/bin/sh\necho ee\n",
			"_runtime/tarantool/tt":        "#!/bin/sh\necho sdk tt\n",
			"_runtime/tt/bin/tt":           "#!/bin/sh\necho tt\n",
		},
	}

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser}, spec.build(t))
	require.NoError(t, err)

	installation := filepath.Join(root, "versions", "tarantool-ee", "tarantool-ee_"+fullVersion)
	assert.Equal(t, "#!/bin/sh\necho ee\n", readFile(t, filepath.Join(installation, "tarantool")))
	assert.NoFileExists(t, filepath.Join(installation, "tt"), "only the product's executable")
	assert.NoDirExists(t, filepath.Join(root, "versions", "tarantool"))

	exe, err := tarantool.Resolve(tarantool.App{
		Scope: binary.ScopeUser, Dir: versionPath(root, "1.0.0"),
	})
	require.NoError(t, err)
	assert.Equal(t, binary.ProgramEe, exe.Program)
}

// TestInstallRuntimeSkipsWhatItCannotLay: a component tt binary could not list,
// or whose installation tt binary left unfinished, is not laid, and the
// install says so instead of failing.
func TestInstallRuntimeSkipsWhatItCannotLay(t *testing.T) {
	root := userRoot(t)

	// An installation of tt 3.2.0 interrupted before its executable landed.
	unfinished := filepath.Join(root, "versions", "tt", "tt_3.2.0")
	require.NoError(t, os.MkdirAll(unfinished, dirPerm))

	spec := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "nightly/build",
		files: rockFiles("my-app", "1.0.0"),
	}

	var warned warnings

	_, err := installInto(t, t.TempDir(), Options{Scope: ScopeUser, Warn: warned.add},
		spec.build(t))
	require.NoError(t, err)

	assert.NoDirExists(t, filepath.Join(root, "versions", "tarantool"))
	assert.NoFileExists(t, filepath.Join(unfinished, "tt"))
	require.Len(t, warned, 2)
	assert.Contains(t, warned[0], `"nightly/build"`)
	assert.Contains(t, warned[1], "tt binary install")
}

// TestInstallProjectLaysRuntimeIntoDotTarantool is decision 17 for the project
// scope: _runtime/ goes to <project>/.tarantool/versions/, there is no
// <project>/_runtime/, and a project that selects no tarantool yet gets the
// bundled one selected, the way tt binary switch selects it, so tt run picks
// it.
func TestInstallProjectLaysRuntimeIntoDotTarantool(t *testing.T) {
	t.Setenv(tarantool.UseSystemEnv, "")

	project := t.TempDir()

	_, err := installInto(t, project, Options{Scope: ScopeProject},
		withRuntimeSpec("my-app", "1.0.0").build(t))
	require.NoError(t, err)

	root := filepath.Join(project, ".tarantool")
	installation := tarantoolInstallation(root)

	assert.FileExists(t, filepath.Join(installation, "tarantool"))
	assert.FileExists(t, filepath.Join(root, "versions", "tt", "tt_3.2.0", "tt"))
	assert.NoDirExists(t, filepath.Join(project, "_runtime"))

	target, err := os.Readlink(filepath.Join(root, "bin", "tarantool"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(installation, "tarantool"), target)

	target, err = os.Readlink(filepath.Join(root, "include", "tarantool"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(installation, "include", "tarantool"), target)

	assert.NoFileExists(t, filepath.Join(root, "bin", "tt"), "only tarantool is selected")

	exe, err := tarantool.Resolve(tarantool.App{Scope: binary.ScopeProject, Dir: project})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(installation, "tarantool"), exe.Path)
	assert.Equal(t, tarantool.OriginProject, exe.Origin)
	assert.Equal(t, fullVersion, exe.Version)
}

// TestInstallProjectKeepsSelectedTarantool: a project that already selects a
// tarantool keeps it; the bundled one is installed next to it and the install
// warns how to select it.
func TestInstallProjectKeepsSelectedTarantool(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".tarantool")

	chosen := filepath.Join(t.TempDir(), "tarantool")
	require.NoError(t, os.WriteFile(chosen, []byte("#!/bin/sh\n"), execPerm))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), dirPerm))
	require.NoError(t, os.Symlink(chosen, filepath.Join(root, "bin", "tarantool")))

	var warned warnings

	_, err := installInto(t, project, Options{Scope: ScopeProject, Warn: warned.add},
		withRuntimeSpec("my-app", "1.0.0").build(t))
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(tarantoolInstallation(root), "tarantool"))

	target, err := os.Readlink(filepath.Join(root, "bin", "tarantool"))
	require.NoError(t, err)
	assert.Equal(t, chosen, target, "the project's own choice stays")
	assert.NoDirExists(t, filepath.Join(root, "include"))

	require.Len(t, warned, 1)
	assert.Contains(t, warned[0], chosen)
	assert.Contains(t, warned[0], "tt binary switch tarantool "+fullVersion+" --scope project")

	// Installing a second package bundling the same runtime again keeps the
	// choice, and a link already selecting the bundled runtime is no news.
	require.NoError(t, os.Remove(filepath.Join(root, "bin", "tarantool")))
	require.NoError(t, os.Symlink(filepath.Join(tarantoolInstallation(root), "tarantool"),
		filepath.Join(root, "bin", "tarantool")))

	warned = nil

	_, err = installInto(t, project, Options{Scope: ScopeProject, Warn: warned.add},
		withRuntimeSpec("other-app", "1.0.0").build(t))
	require.NoError(t, err)
	assert.Empty(t, warned)
}

// TestInstallProjectRuntimeWithoutHeaders: a runtime without headers is
// selected by its executable alone, as tt binary would refuse to switch to it.
func TestInstallProjectRuntimeWithoutHeaders(t *testing.T) {
	project := t.TempDir()

	spec := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: fullVersion,
		files:   rockFiles("my-app", "1.0.0"),
		runtime: map[string]string{"_runtime/tarantool/bin/tarantool": "#!/bin/sh\n"},
	}

	_, err := installInto(t, project, Options{Scope: ScopeProject}, spec.build(t))
	require.NoError(t, err)

	root := filepath.Join(project, ".tarantool")

	target, err := os.Readlink(filepath.Join(root, "bin", "tarantool"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tarantoolInstallation(root), "tarantool"), target)
	assert.NoDirExists(t, filepath.Join(root, "include"))
}
