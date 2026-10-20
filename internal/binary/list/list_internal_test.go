package list

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func listExecutable(t *testing.T, root string, program binary.Program, name string) string {
	t.Helper()

	bin := filepath.Join(root, binary.VersionsDirName, program.String(),
		program.String()+"_"+name, program.Exec())
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, nil, 0o755))

	return bin
}

func versionNames(versions []version.Version) []string {
	names := make([]string, len(versions))
	for i, ver := range versions {
		names[i] = ver.Str
	}

	return names
}

func TestInstalledVersionsOrdering(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"v2.8.6", "2.10.5", "master", "0000000", "aaaaaaa", "1.10.0"} {
		bin := listExecutable(t, root, binary.ProgramCe, name)
		if name == "v2.8.6" {
			require.NoError(t, os.MkdirAll(filepath.Join(root, binary.BinDirName), 0o755))
			require.NoError(t, os.Symlink(bin, filepath.Join(root, binary.BinDirName, "tarantool")))
		}
	}

	versions, err := installedVersions(root, binary.ProgramCe)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"master", "2.10.5", "v2.8.6 [active]", "1.10.0", "0000000", "aaaaaaa",
	}, versionNames(versions))
}

func TestInstalledVersionsWithLocalBuild(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "dangling"}[dangling], func(t *testing.T) {
			root := t.TempDir()
			listExecutable(t, root, binary.ProgramCe, "1.10.0")
			listExecutable(t, root, binary.ProgramCe, "2.10.7")

			buildPath := filepath.Join(t.TempDir(), "tarantool")
			if !dangling {
				require.NoError(t, os.WriteFile(buildPath, nil, 0o755))
			}

			require.NoError(t, os.MkdirAll(filepath.Join(root, binary.BinDirName), 0o755))
			require.NoError(t, os.Symlink(buildPath,
				filepath.Join(root, binary.BinDirName, "tarantool")))

			versions, err := installedVersions(root, binary.ProgramCe)
			require.NoError(t, err)
			assert.Equal(t, []string{"2.10.7", "1.10.0"}, versionNames(versions))
		})
	}
}

func TestInstalledVersionsRegularTarantool(t *testing.T) {
	root := t.TempDir()
	listExecutable(t, root, binary.ProgramCe, "2.10.1")
	listExecutable(t, root, binary.ProgramEe, "2.11.1")

	bin := filepath.Join(root, binary.BinDirName, "tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin,
		[]byte("#!/bin/sh\necho 'Tarantool 3.1.0-entrypoint-83-gcb0264c3c'\n"), 0o755))

	versions, err := installedVersions(root, binary.ProgramCe)
	require.NoError(t, err)
	assert.Equal(t, []string{"3.1.0-entrypoint-83-gcb0264c3c [active]", "2.10.1"},
		versionNames(versions))

	versions, err = installedVersions(root, binary.ProgramEe)
	require.NoError(t, err)
	assert.Equal(t, []string{"2.11.1"}, versionNames(versions))

	// EE never executes the regular shared Tarantool binary to detect its version.
	require.NoError(t, os.Chmod(bin, 0o440))

	versions, err = installedVersions(root, binary.ProgramEe)
	require.NoError(t, err)
	assert.Equal(t, []string{"2.11.1"}, versionNames(versions))

	_, err = installedVersions(root, binary.ProgramCe)
	require.ErrorContains(t, err, "failed to get tarantool version")
}

func TestDisplayVersionInvalid(t *testing.T) {
	_, err := displayVersion("invalid")
	require.Error(t, err)
}

func TestListBinariesEmptyScope(t *testing.T) {
	for _, emptyRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[emptyRoot], func(t *testing.T) {
			project := t.TempDir()
			t.Chdir(project)

			if emptyRoot {
				require.NoError(t, os.Mkdir(filepath.Join(project, ".tarantool"), 0o755))
			}

			err := ListBinaries(ListCtx{Scope: binary.ScopeProject})
			require.ErrorIs(t, err, ErrNoBinariesInstalled)

			if !emptyRoot {
				_, err = os.Stat(filepath.Join(project, ".tarantool"))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestListBinariesIgnoresParent(t *testing.T) {
	parent := t.TempDir()
	listExecutable(t, filepath.Join(parent, ".tarantool"), binary.ProgramTt, "v3.0.0")

	child := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(child, 0o755))
	t.Chdir(child)

	err := ListBinaries(ListCtx{Scope: binary.ScopeProject})
	require.ErrorIs(t, err, ErrNoBinariesInstalled)
}

func TestListBinariesDirectoryError(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	require.NoError(t, os.Mkdir(filepath.Join(project, ".tarantool"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, ".tarantool", "versions"), nil, 0o644))

	err := ListBinaries(ListCtx{Scope: binary.ScopeProject})
	require.ErrorContains(t, err, "error reading directory")
	assert.NotErrorIs(t, err, ErrNoBinariesInstalled)
}

func TestListBinariesInvalidScope(t *testing.T) {
	err := ListBinaries(ListCtx{})
	require.ErrorIs(t, err, binary.ErrUnknownScope)
}

func TestParseBinariesCompleteInstallations(t *testing.T) {
	root := t.TempDir()
	bin := listExecutable(t, root, binary.ProgramTt, "v3.0.0")
	dir := filepath.Join(root, binary.VersionsDirName, "tt")

	for _, name := range []string{"tt_", "tt_1.0.0", "other_1.0.0", "tt_2.0.0/tt"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
	}

	require.NoError(t, os.Symlink(filepath.Dir(bin), filepath.Join(dir, "tt_4.0.0")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tt_5.0.0"), nil, 0o644))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	versions, err := ParseBinaries(entries, binary.ProgramTt, root)
	require.NoError(t, err)
	assert.Equal(t, []string{"v3.0.0"}, versionNames(versions))
}

func TestParseBinariesActiveIdentity(t *testing.T) {
	for _, relative := range []bool{false, true} {
		t.Run(map[bool]string{false: "absolute", true: "relative"}[relative], func(t *testing.T) {
			root := t.TempDir()
			listExecutable(t, root, binary.ProgramCe, "3.0.0")

			target := listExecutable(t, root, binary.ProgramEe, "3.0.0")
			listExecutable(t, root, binary.ProgramEe, "3.1.0")

			if relative {
				var err error

				target, err = filepath.Rel(filepath.Join(root, binary.BinDirName), target)
				require.NoError(t, err)
			}

			require.NoError(t, os.MkdirAll(filepath.Join(root, binary.BinDirName), 0o755))
			require.NoError(t, os.Symlink(target,
				filepath.Join(root, binary.BinDirName, "tarantool")))

			ce, err := installedVersions(root, binary.ProgramCe)
			require.NoError(t, err)
			assert.Equal(t, []string{"3.0.0"}, versionNames(ce))

			ee, err := installedVersions(root, binary.ProgramEe)
			require.NoError(t, err)
			assert.Equal(t, []string{"3.1.0", "3.0.0 [active]"}, versionNames(ee))
		})
	}
}

func TestTarantoolBuildPathManagedAliases(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramCe, binary.ProgramEe} {
		for _, dangling := range []bool{false, true} {
			suffix := map[bool]string{false: "/existing", true: "/dangling"}[dangling]
			name := program.String() + suffix
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				target := listExecutable(t, root, program, "3.0.0")
				alias := filepath.Join(t.TempDir(), "scope")
				require.NoError(t, os.Symlink(root, alias))
				require.NoError(t, os.MkdirAll(filepath.Join(root, binary.BinDirName), 0o755))

				if dangling {
					require.NoError(t, os.RemoveAll(filepath.Join(root, binary.VersionsDirName)))
				}

				link := filepath.Join(root, binary.BinDirName, "tarantool")
				require.NoError(t, os.Symlink(target, link))

				build, err := binary.TarantoolBuildPath(filepath.Join(alias, binary.BinDirName))
				require.NoError(t, err)
				assert.Empty(t, build)

				suffix, err := filepath.Rel(root, target)
				require.NoError(t, err)
				require.NoError(t, os.Remove(link))
				require.NoError(t, os.Symlink(filepath.Join(alias, suffix), link))

				build, err = binary.TarantoolBuildPath(filepath.Join(root, binary.BinDirName))
				require.NoError(t, err)
				assert.Empty(t, build)
			})
		}
	}
}

func TestTarantoolBuildPathOtherScope(t *testing.T) {
	root := t.TempDir()
	target := listExecutable(t, t.TempDir(), binary.ProgramCe, "3.0.0")
	listExecutable(t, root, binary.ProgramCe, "3.0.0")
	require.NoError(t, os.MkdirAll(filepath.Join(root, binary.BinDirName), 0o755))
	require.NoError(t, os.Symlink(target,
		filepath.Join(root, binary.BinDirName, "tarantool")))

	build, err := binary.TarantoolBuildPath(filepath.Join(root, binary.BinDirName))
	require.NoError(t, err)
	assert.Equal(t, target, build)

	versions, err := installedVersions(root, binary.ProgramCe)
	require.NoError(t, err)
	assert.Equal(t, []string{"3.0.0"}, versionNames(versions))
}
