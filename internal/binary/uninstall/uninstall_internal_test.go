package uninstall

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func uninstallContext(scope binary.Scope, program binary.Program, version string) UninstallCtx {
	var ctx UninstallCtx

	ctx.Scope = scope
	ctx.Program = program
	ctx.Version = version

	return ctx
}

func installVersion(t *testing.T, root string, program binary.Program, version string) string {
	t.Helper()

	dir := filepath.Join(root, binary.VersionsDirName, program.String(),
		program.String()+"_"+version)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, program.Exec()), []byte("binary"), 0o755))

	if program.IsTarantool() {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "include", "tarantool"), 0o755))
	}

	return dir
}

func linkInstallation(t *testing.T, root string, program binary.Program, installation string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	require.NoError(t, binary.CreateSymlink(filepath.Join(installation, program.Exec()),
		filepath.Join(root, "bin", program.Exec()), true))

	if program.IsTarantool() {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "include"), 0o755))
		require.NoError(t, binary.CreateSymlink(filepath.Join(installation, "include", "tarantool"),
			filepath.Join(root, "include", "tarantool"), true))
	}
}

func assertLink(t *testing.T, link, target string) {
	t.Helper()

	actual, err := filepath.EvalSymlinks(link)
	require.NoError(t, err)

	expected, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
}

func assertMissing(t *testing.T, path string) {
	t.Helper()

	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func projectRoot(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	return root
}

func TestGetListAndDefault(t *testing.T) {
	root := projectRoot(t)
	installation := installVersion(t, root, binary.ProgramTt, "v3.0.0")
	dir := filepath.Dir(installation)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "unrelated"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "tt_"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "tarantool_4.0.0"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tt_v4.0.0"), nil, 0o644))
	require.NoError(t, os.Symlink(installation, filepath.Join(dir, "tt_v5.0.0")))
	assert.Equal(t, []string{"v3.0.0"}, GetList(binary.ScopeProject, "tt"))

	selected, err := getDefault(binary.ProgramTt, root)
	require.NoError(t, err)
	assert.Equal(t, "v3.0.0", selected)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "tt_master"), 0o755))
	assert.Equal(t, []string{"master", "v3.0.0"}, GetList(binary.ScopeProject, "tt"))

	_, err = getDefault(binary.ProgramTt, root)
	require.ErrorIs(t, err, errMultipleInstalledVersions)
	assert.Empty(t, GetList(binary.ScopeUnknown, "tt"))
	assert.Empty(t, GetList(binary.ScopeProject, "nodejs"))
	assert.Empty(t, GetList(binary.ScopeProject, "tarantool-sdk"))
}

func TestUninstallEmptyScope(t *testing.T) {
	root := projectRoot(t)
	ctx := uninstallContext(binary.ScopeProject, binary.ProgramTt, "")
	require.ErrorIs(t, UninstallProgram(ctx), errHasNoInstalledVersion)
	assert.Empty(t, GetList(binary.ScopeProject, "tt"))

	ctx.Version = "3.0.0"
	require.ErrorIs(t, UninstallProgram(ctx), errNotInstalled)
	assertMissing(t, root)
}

func TestUninstallLastVersion(t *testing.T) {
	for _, program := range []binary.Program{
		binary.ProgramTt, binary.ProgramCe, binary.ProgramEe, binary.ProgramTcm,
	} {
		for _, requested := range []string{"", "3.0.0"} {
			t.Run(program.String()+"/"+requested, func(t *testing.T) {
				root := projectRoot(t)
				installation := installVersion(t, root, program, "3.0.0")
				require.NoError(t, os.WriteFile(filepath.Join(installation, "extra"), nil, 0o644))
				linkInstallation(t, root, program, installation)

				ctx := uninstallContext(binary.ScopeProject, program, requested)
				require.NoError(t, UninstallProgram(ctx))
				assertMissing(t, installation)
				assertMissing(t, filepath.Join(root, "bin", program.Exec()))

				if program.IsTarantool() {
					assertMissing(t, filepath.Join(root, "include", "tarantool"))
				}
			})
		}
	}
}

func TestUninstallInactiveVersion(t *testing.T) {
	for _, program := range []binary.Program{
		binary.ProgramTt, binary.ProgramCe, binary.ProgramEe, binary.ProgramTcm,
	} {
		t.Run(program.String(), func(t *testing.T) {
			root := projectRoot(t)
			active := installVersion(t, root, program, "2.0.0")
			removed := installVersion(t, root, program, "3.0.0")
			linkInstallation(t, root, program, active)

			ctx := uninstallContext(binary.ScopeProject, program, "3.0.0")
			require.NoError(t, UninstallProgram(ctx))
			assertMissing(t, removed)
			assertLink(t, filepath.Join(root, "bin", program.Exec()),
				filepath.Join(active, program.Exec()))

			if program.IsTarantool() {
				assertLink(t, filepath.Join(root, "include", "tarantool"),
					filepath.Join(active, "include", "tarantool"))
			}
		})
	}
}

func TestUninstallBundleVersionRequests(t *testing.T) {
	testCases := []struct {
		name      string
		program   binary.Program
		installed []string
		requested string
		removed   string
		wantErr   error
	}{
		{"ee_major", binary.ProgramEe, []string{"gc64-3.8.1-0-r60"}, "3",
			"", errNotInstalled},
		{"ee_minor", binary.ProgramEe, []string{"gc64-3.8.1-0-r60"}, "3.8",
			"", errNotInstalled},
		{"ee_patch", binary.ProgramEe, []string{"gc64-3.8.1-0-r60"}, "3.8.1",
			"gc64-3.8.1-0-r60", nil},
		{"ee_nogc64", binary.ProgramEe, []string{"nogc64-3.8.1-0-r60"}, "3.8.1",
			"nogc64-3.8.1-0-r60", nil},
		{"ee_rc", binary.ProgramEe, []string{"gc64-3.8.1-rc1-0-r60"}, "3.8.1",
			"gc64-3.8.1-rc1-0-r60", nil},
		{"ee_full", binary.ProgramEe, []string{"gc64-3.8.1-0-r60", "nogc64-3.8.1-0-r60"},
			"nogc64-3.8.1-0-r60", "nogc64-3.8.1-0-r60", nil},
		{"ee_full_rc", binary.ProgramEe, []string{"gc64-3.8.1-0-r60", "gc64-3.8.1-rc1-0-r60"},
			"gc64-3.8.1-rc1-0-r60", "gc64-3.8.1-rc1-0-r60", nil},
		{"tcm_major", binary.ProgramTcm, []string{"3.8.1-0-r60"}, "3", "", errNotInstalled},
		{"tcm_minor", binary.ProgramTcm, []string{"3.8.1-0-r60"}, "3.8", "", errNotInstalled},
		{"tcm_patch", binary.ProgramTcm, []string{"3.8.1-0-r60"}, "3.8.1", "3.8.1-0-r60", nil},
		{"tcm_full", binary.ProgramTcm, []string{"3.8.1-0-r60", "3.8.1-0-r61"},
			"3.8.1-0-r60", "3.8.1-0-r60", nil},
		{"ee_variants", binary.ProgramEe, []string{"gc64-3.8.1-0-r60", "nogc64-3.8.1-0-r60"},
			"3.8.1", "", version.ErrAmbiguousInstalledVersion},
		{"ee_canonical_and_legacy", binary.ProgramEe, []string{"3.8.1", "gc64-3.8.1-0-r60"},
			"3.8.1", "3.8.1", nil},
		{"ee_revisions", binary.ProgramEe, []string{"gc64-3.8.1-0-r60", "gc64-3.8.1-0-r61"},
			"3.8.1", "", version.ErrAmbiguousInstalledVersion},
		{"ee_major_rejected", binary.ProgramEe, []string{"gc64-3.8.1-0-r60", "gc64-3.9.0-0-r60"},
			"3", "", errNotInstalled},
		{"tcm_revisions", binary.ProgramTcm, []string{"3.8.1-0-r60", "3.8.1-0-r61"},
			"3.8.1", "", version.ErrAmbiguousInstalledVersion},
		{"tcm_plain_and_revision", binary.ProgramTcm, []string{"3.8.1", "3.8.1-0-r60"},
			"3.8.1", "", version.ErrAmbiguousInstalledVersion},
		{"ee_partial_build", binary.ProgramEe, []string{"gc64-3.8.1-0-r60"},
			"gc64-3.8.1", "", errNotInstalled},
		{"tcm_partial_release", binary.ProgramTcm, []string{"3.8.1-rc1-0-r60"},
			"3.8.1-rc1", "", errNotInstalled},
		{"missing_version", binary.ProgramEe, []string{"gc64-3.8.1-0-r60"},
			"4.0.0", "", errNotInstalled},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := projectRoot(t)
			active := installVersion(t, root, testCase.program, "2.7.0")
			linkInstallation(t, root, testCase.program, active)

			for _, installed := range testCase.installed {
				installVersion(t, root, testCase.program, installed)
			}

			if testCase.wantErr != nil {
				active = filepath.Join(root, binary.VersionsDirName, testCase.program.String(),
					testCase.program.String()+"_"+testCase.installed[0])
				linkInstallation(t, root, testCase.program, active)
			}

			before := snapshotScope(t, root)
			ctx := uninstallContext(binary.ScopeProject, testCase.program, testCase.requested)
			err := UninstallProgram(ctx)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.Equal(t, before, snapshotScope(t, root))

				if errors.Is(testCase.wantErr, version.ErrAmbiguousInstalledVersion) {
					for _, installed := range testCase.installed {
						require.ErrorContains(t, err, installed)
					}
				}

				return
			}

			require.NoError(t, err)

			for _, installed := range testCase.installed {
				path := filepath.Join(root, binary.VersionsDirName, testCase.program.String(),
					testCase.program.String()+"_"+installed)
				if installed == testCase.removed {
					assertMissing(t, path)
				} else {
					assert.DirExists(t, path)
				}
			}

			assertLink(t, filepath.Join(root, "bin", testCase.program.Exec()),
				filepath.Join(active, testCase.program.Exec()))

			if testCase.program.IsTarantool() {
				assertLink(t, filepath.Join(root, "include", "tarantool"),
					filepath.Join(active, "include", "tarantool"))
			}
		})
	}
}

func TestUninstallIncompleteBundle(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramEe, binary.ProgramTcm} {
		fullVersion := "3.8.1-0-r60"
		if program == binary.ProgramEe {
			fullVersion = "gc64-" + fullVersion
		}

		for _, requested := range []string{"", "3.8.1", fullVersion} {
			t.Run(program.String()+"/"+requested, func(t *testing.T) {
				root := projectRoot(t)
				installed := installVersion(t, root, program, fullVersion)
				linkInstallation(t, root, program, installed)
				require.NoError(t, os.Remove(filepath.Join(installed, program.Exec())))
				require.NoError(t, os.RemoveAll(filepath.Join(installed, "include")))

				ctx := uninstallContext(binary.ScopeProject, program, requested)
				require.NoError(t, UninstallProgram(ctx))
				assertMissing(t, installed)
				assertMissing(t, filepath.Join(root, "bin", program.Exec()))

				if program.IsTarantool() {
					assertMissing(t, filepath.Join(root, "include", "tarantool"))
				}
			})
		}
	}
}

func TestUninstallBundleDefaultRemainsUnambiguous(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramEe, binary.ProgramTcm} {
		t.Run(program.String(), func(t *testing.T) {
			root := projectRoot(t)
			active := installVersion(t, root, program, "3.8.0-0-r60")
			installVersion(t, root, program, "3.8.1-0-r60")
			linkInstallation(t, root, program, active)

			before := snapshotScope(t, root)
			ctx := uninstallContext(binary.ScopeProject, program, "")
			require.ErrorIs(t, UninstallProgram(ctx), errMultipleInstalledVersions)
			assert.Equal(t, before, snapshotScope(t, root))
		})
	}
}

func TestUninstallBundleVersionScopeIsolation(t *testing.T) {
	for _, program := range []binary.Program{binary.ProgramEe, binary.ProgramTcm} {
		t.Run(program.String(), func(t *testing.T) {
			root := projectRoot(t)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())

			userRoot, err := binary.ScopeUser.ArtifactPath()
			require.NoError(t, err)

			user := installVersion(t, userRoot, program, "3.8.1-0-r60")
			linkInstallation(t, userRoot, program, user)

			ctx := uninstallContext(binary.ScopeProject, program, "3.8.1")
			require.ErrorIs(t, UninstallProgram(ctx), errNotInstalled)
			assertMissing(t, root)

			local := installVersion(t, root, program, "3.8.1-0-r61")
			linkInstallation(t, root, program, local)

			userBefore := snapshotScope(t, userRoot)
			require.NoError(t, UninstallProgram(ctx))
			assertMissing(t, local)
			assert.Equal(t, userBefore, snapshotScope(t, userRoot))
		})
	}
}

func TestUninstallPreservesOtherEdition(t *testing.T) {
	for _, removedProgram := range []binary.Program{binary.ProgramCe, binary.ProgramEe} {
		t.Run(removedProgram.String(), func(t *testing.T) {
			root := projectRoot(t)
			activeProgram := binary.ProgramEe

			if removedProgram == binary.ProgramEe {
				activeProgram = binary.ProgramCe
			}

			active := installVersion(t, root, activeProgram, "3.0.0")
			removed := installVersion(t, root, removedProgram, "3.0.0")
			linkInstallation(t, root, activeProgram, active)

			ctx := uninstallContext(binary.ScopeProject, removedProgram, "3.0.0")
			require.NoError(t, UninstallProgram(ctx))
			assertMissing(t, removed)
			assertLink(t, filepath.Join(root, "bin", "tarantool"),
				filepath.Join(active, "tarantool"))
			assertLink(t, filepath.Join(root, "include", "tarantool"),
				filepath.Join(active, "include", "tarantool"))
		})
	}
}

func TestUninstallTtVersionFormats(t *testing.T) {
	testCases := []struct {
		name      string
		versions  []string
		requested string
		removed   string
	}{
		{"exact_plain", []string{"3.0.0", "v3.0.0"}, "3.0.0", "3.0.0"},
		{"exact_prefixed", []string{"3.0.0", "v3.0.0"}, "v3.0.0", "v3.0.0"},
		{"plain_fallback", []string{"v3.0.0"}, "3.0.0", "v3.0.0"},
		{"no_reverse_fallback", []string{"3.0.0"}, "v3.0.0", ""},
		{"master", []string{"master"}, "master", "master"},
		{"commit", []string{"abcdef0"}, "abcdef0", "abcdef0"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := projectRoot(t)
			for _, ver := range testCase.versions {
				installVersion(t, root, binary.ProgramTt, ver)
			}

			ctx := uninstallContext(binary.ScopeProject, binary.ProgramTt, testCase.requested)
			err := UninstallProgram(ctx)

			if testCase.removed == "" {
				require.ErrorIs(t, err, errNotInstalled)
			} else {
				require.NoError(t, err)
			}

			for _, ver := range testCase.versions {
				path := filepath.Join(root, "versions", "tt", "tt_"+ver)
				if ver == testCase.removed {
					assertMissing(t, path)

					continue
				}

				assert.DirExists(t, path)
			}
		})
	}
}

func TestSearchLatestVersion(t *testing.T) {
	type candidate struct {
		program binary.Program
		version string
		missing string
	}

	testCases := []struct {
		name       string
		program    binary.Program
		candidates []candidate
		want       string
	}{
		{"release_over_hash", binary.ProgramTt, []candidate{
			{binary.ProgramTt, "aaaaaaa", ""}, {binary.ProgramTt, "v2.0.0", ""},
			{binary.ProgramTt, "3.0.0", ""}, {binary.ProgramTt, "master", ""},
		}, "tt_3.0.0"},
		{"hash_lexical", binary.ProgramTcm, []candidate{
			{binary.ProgramTcm, "bbbbbbb", ""}, {binary.ProgramTcm, "aaaaaaa", ""},
		}, "tcm_aaaaaaa"},
		{"master_excluded", binary.ProgramCe, []candidate{
			{binary.ProgramCe, "master", ""},
		}, ""},
		{"ce_ignores_ee", binary.ProgramCe, []candidate{
			{binary.ProgramCe, "2.0.0", ""}, {binary.ProgramEe, "3.0.0", ""},
		}, "tarantool_2.0.0"},
		{"ee_accepts_ce", binary.ProgramEe, []candidate{
			{binary.ProgramEe, "2.0.0", ""}, {binary.ProgramCe, "3.0.0", ""},
		}, "tarantool_3.0.0"},
		{"equal_releases_ee_first", binary.ProgramEe, []candidate{
			{binary.ProgramCe, "3.0.0", ""}, {binary.ProgramEe, "3.0.0", ""},
		}, "tarantool-ee_3.0.0"},
		{"hashes_ee_first", binary.ProgramEe, []candidate{
			{binary.ProgramCe, "aaaaaaa", ""}, {binary.ProgramEe, "bbbbbbb", ""},
		}, "tarantool-ee_bbbbbbb"},
		{"missing_headers", binary.ProgramCe, []candidate{
			{binary.ProgramCe, "3.0.0", "headers"}, {binary.ProgramCe, "2.0.0", ""},
		}, "tarantool_2.0.0"},
		{"headers_are_file", binary.ProgramCe, []candidate{
			{binary.ProgramCe, "3.0.0", "headers_file"}, {binary.ProgramCe, "aaaaaaa", ""},
		}, "tarantool_aaaaaaa"},
		{"hash_without_headers", binary.ProgramCe, []candidate{
			{binary.ProgramCe, "aaaaaaa", "headers"}, {binary.ProgramCe, "bbbbbbb", ""},
		}, "tarantool_bbbbbbb"},
		{"incomplete_binary", binary.ProgramTt, []candidate{
			{binary.ProgramTt, "v3.0.0", "binary"}, {binary.ProgramTt, "abcdef0", ""},
		}, "tt_abcdef0"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			for _, candidate := range testCase.candidates {
				dir := installVersion(t, root, candidate.program, candidate.version)
				headers := filepath.Join(dir, "include", "tarantool")

				switch candidate.missing {
				case "binary":
					require.NoError(t, os.Remove(filepath.Join(dir, candidate.program.Exec())))
				case "headers":
					require.NoError(t, os.Remove(headers))
				case "headers_file":
					require.NoError(t, os.Remove(headers))
					require.NoError(t, os.WriteFile(headers, nil, 0o644))
				}
			}

			selected, err := searchLatestVersion(testCase.program, root)
			require.NoError(t, err)

			if testCase.want == "" {
				assert.Empty(t, selected.ID)

				return
			}

			assert.Equal(t, testCase.want, binary.InstallationName(selected.Program, selected.ID))
		})
	}
}

func TestUninstallActiveSelectsReplacement(t *testing.T) {
	for _, program := range []binary.Program{
		binary.ProgramTt, binary.ProgramCe, binary.ProgramEe, binary.ProgramTcm,
	} {
		t.Run(program.String(), func(t *testing.T) {
			root := projectRoot(t)
			active := installVersion(t, root, program, "2.0.0")
			selected := installVersion(t, root, program, "3.0.0")
			linkInstallation(t, root, program, active)

			ctx := uninstallContext(binary.ScopeProject, program, "2.0.0")
			require.NoError(t, UninstallProgram(ctx))
			assertMissing(t, active)
			assertLink(t, filepath.Join(root, "bin", program.Exec()),
				filepath.Join(selected, program.Exec()))

			if program.IsTarantool() {
				assertLink(t, filepath.Join(root, "include", "tarantool"),
					filepath.Join(selected, "include", "tarantool"))
			}
		})
	}
}

func TestUninstallIncompleteWithRelativeAliasLinks(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		name := map[bool]string{false: "complete", true: "incomplete"}[incomplete]
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			actual := t.TempDir()
			root := filepath.Join(project, ".tarantool")
			require.NoError(t, os.Symlink(actual, root))
			t.Chdir(project)

			installation := installVersion(t, actual, binary.ProgramCe, "3.0.0")
			require.NoError(t, os.MkdirAll(filepath.Join(actual, "bin"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(actual, "include"), 0o755))
			require.NoError(t, os.Symlink("../versions/tarantool/tarantool_3.0.0/tarantool",
				filepath.Join(actual, "bin", "tarantool")))
			require.NoError(t, os.Symlink(filepath.Join(actual, "versions", "tarantool",
				"tarantool_3.0.0", "include", "tarantool"),
				filepath.Join(root, "include", "tarantool")))

			if incomplete {
				require.NoError(t, os.Remove(filepath.Join(installation, "tarantool")))
				require.NoError(t, os.RemoveAll(filepath.Join(installation, "include")))
			}

			ctx := uninstallContext(binary.ScopeProject, binary.ProgramCe, "")
			require.NoError(t, UninstallProgram(ctx))
			assertMissing(t, installation)
			assertMissing(t, filepath.Join(root, "bin", "tarantool"))
			assertMissing(t, filepath.Join(root, "include", "tarantool"))
		})
	}
}

func TestUninstallBuildPreservesSourcesAndSelectsCe(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "dangling"}[dangling], func(t *testing.T) {
			root := projectRoot(t)
			build := installVersion(t, t.TempDir(), binary.ProgramCe, "master")
			linkInstallation(t, root, binary.ProgramCe, build)

			selected := installVersion(t, root, binary.ProgramCe, "3.0.0")
			installVersion(t, root, binary.ProgramCe, "2.0.0")
			installVersion(t, root, binary.ProgramEe, "4.0.0")

			if dangling {
				require.NoError(t, os.RemoveAll(build))
			}

			ctx := uninstallContext(binary.ScopeProject, binary.ProgramCe, "")
			require.NoError(t, UninstallProgram(ctx))
			assertLink(t, filepath.Join(root, "bin", "tarantool"),
				filepath.Join(selected, "tarantool"))
			assertLink(t, filepath.Join(root, "include", "tarantool"),
				filepath.Join(selected, "include", "tarantool"))
			assert.DirExists(t, selected)

			if !dangling {
				assert.FileExists(t, filepath.Join(build, "tarantool"))
				assert.DirExists(t, filepath.Join(build, "include", "tarantool"))
			}
		})
	}
}

func TestUninstallInactivePreservesBuild(t *testing.T) {
	root := projectRoot(t)
	build := installVersion(t, t.TempDir(), binary.ProgramCe, "master")
	linkInstallation(t, root, binary.ProgramCe, build)

	removed := installVersion(t, root, binary.ProgramCe, "3.0.0")
	ctx := uninstallContext(binary.ScopeProject, binary.ProgramCe, "3.0.0")
	require.NoError(t, UninstallProgram(ctx))
	assertMissing(t, removed)
	assertLink(t, filepath.Join(root, "bin", "tarantool"), filepath.Join(build, "tarantool"))
	assertLink(t, filepath.Join(root, "include", "tarantool"),
		filepath.Join(build, "include", "tarantool"))
}

func TestUninstallScopeIsolation(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(child, 0o755))
	t.Chdir(child)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	root, err := binary.ScopeUser.ArtifactPath()
	require.NoError(t, err)

	user := installVersion(t, root, binary.ProgramTt, "v3.0.0")
	ancestor := installVersion(t, filepath.Join(parent, ".tarantool"), binary.ProgramTt, "v3.0.0")
	linkInstallation(t, root, binary.ProgramTt, user)

	ctx := uninstallContext(binary.ScopeProject, binary.ProgramTt, "3.0.0")
	require.ErrorIs(t, UninstallProgram(ctx), errNotInstalled)
	assert.Empty(t, GetList(binary.ScopeProject, "tt"))
	assertMissing(t, filepath.Join(child, ".tarantool"))
	assert.DirExists(t, ancestor)
	assert.DirExists(t, user)

	project := installVersion(t, filepath.Join(child, ".tarantool"), binary.ProgramTt, "v4.0.0")

	ctx.Scope = binary.ScopeUser
	ctx.Version = "4.0.0"
	require.ErrorIs(t, UninstallProgram(ctx), errNotInstalled)
	assert.Equal(t, []string{"v3.0.0"}, GetList(binary.ScopeUser, "tt"))

	ctx.Version = "3.0.0"
	require.NoError(t, UninstallProgram(ctx))
	assertMissing(t, user)
	assertMissing(t, filepath.Join(root, "bin", "tt"))
	assert.DirExists(t, project)
	assert.DirExists(t, ancestor)
}

func TestUninstallInvalidContext(t *testing.T) {
	root := projectRoot(t)
	installed := installVersion(t, root, binary.ProgramTt, "v3.0.0")
	linkInstallation(t, root, binary.ProgramTt, installed)

	for _, requested := range []string{".", "..", "3.0.0/../../outside", "/tmp/tt", "bad\\name"} {
		ctx := uninstallContext(binary.ScopeProject, binary.ProgramTt, requested)
		require.ErrorIs(t, UninstallProgram(ctx), errInvalidVersion)
		assert.DirExists(t, installed)
		assertLink(t, filepath.Join(root, "bin", "tt"), filepath.Join(installed, "tt"))
	}

	ctx := uninstallContext(binary.ScopeUnknown, binary.ProgramTt, "3.0.0")
	require.ErrorIs(t, UninstallProgram(ctx), binary.ErrUnknownScope)

	ctx.Scope = binary.ScopeProject
	ctx.Program = binary.ProgramSDK
	require.ErrorIs(t, UninstallProgram(ctx), errNotInstalled)
}

func TestGetAllVersionFormats(t *testing.T) {
	type testCase struct {
		name             string
		program          binary.Program
		ttVersion        string
		expectedVersions []string
		expectedError    error
	}

	cases := []testCase{
		{
			name:             "without prefix",
			program:          binary.ProgramTt,
			ttVersion:        "1.2.3",
			expectedVersions: []string{"1.2.3", "v1.2.3"},
			expectedError:    nil,
		},
		{
			name:             "with prefix",
			program:          binary.ProgramTt,
			ttVersion:        "v1.2.3",
			expectedVersions: []string{"v1.2.3"},
			expectedError:    nil,
		},
		{
			name:             "not tt program",
			program:          binary.ProgramCe,
			ttVersion:        "1.2.3",
			expectedVersions: []string{"1.2.3"},
			expectedError:    nil,
		},
		{
			name:             "not <major.minor.patch> format",
			program:          binary.ProgramCe,
			ttVersion:        "e902206",
			expectedVersions: []string{"e902206"},
			expectedError:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ttVersions, err := getAllTtVersionFormats(tc.program, tc.ttVersion)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedVersions, ttVersions)
		})
	}
}

// snapshotScope records entries without following links to another scope.
func snapshotScope(t *testing.T, root string) map[string]string {
	t.Helper()

	entries := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		switch {
		case entry.IsDir():
			entries[path] = "directory"
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			require.NoError(t, err)

			entries[path] = "link:" + target
		default:
			// The fixture tree is owned by this test and cannot change concurrently.
			data, err := os.ReadFile(path) //nolint:gosec
			require.NoError(t, err)

			entries[path] = string(data)
		}

		return nil
	})
	require.NoError(t, err)

	return entries
}

func TestUninstallRejectsEscapingDirectories(t *testing.T) {
	for _, directory := range []string{"versions", "versions/tarantool-ee",
		"versions/tarantool", "bin", "include"} {
		t.Run(directory, func(t *testing.T) {
			root := projectRoot(t)
			other := t.TempDir()

			for _, scope := range []string{root, other} {
				active := installVersion(t, scope, binary.ProgramEe, "3.0.0")
				installVersion(t, scope, binary.ProgramCe, "2.0.0")
				linkInstallation(t, scope, binary.ProgramEe, active)
			}

			link := filepath.Join(root, directory)
			require.NoError(t, os.Rename(link, link+".saved"))
			require.NoError(t, os.Symlink(filepath.Join(other, directory), link))

			beforeRoot := snapshotScope(t, root)
			beforeOther := snapshotScope(t, other)
			ctx := uninstallContext(binary.ScopeProject, binary.ProgramEe, "3.0.0")
			require.ErrorIs(t, UninstallProgram(ctx), errOutsideScope)
			assert.Equal(t, beforeRoot, snapshotScope(t, root))
			assert.Equal(t, beforeOther, snapshotScope(t, other))

			if directory == "versions" || directory == "versions/tarantool-ee" {
				assert.Empty(t, GetList(binary.ScopeProject, "tarantool-ee"))

				_, err := getDefault(binary.ProgramEe, root)
				require.ErrorIs(t, err, errOutsideScope)
			}

			if directory == "versions/tarantool" {
				_, err := searchLatestVersion(binary.ProgramEe, root)
				require.ErrorIs(t, err, errOutsideScope)
			}
		})
	}
}

func TestUninstallDefaultPreservesManagedEeLink(t *testing.T) {
	root := projectRoot(t)
	active := installVersion(t, root, binary.ProgramEe, "3.0.0")
	linkInstallation(t, root, binary.ProgramEe, active)

	ctx := uninstallContext(binary.ScopeProject, binary.ProgramCe, "")
	require.ErrorIs(t, UninstallProgram(ctx), errHasNoInstalledVersion)
	assertLink(t, filepath.Join(root, "bin", "tarantool"), filepath.Join(active, "tarantool"))
	assertLink(t, filepath.Join(root, "include", "tarantool"),
		filepath.Join(active, "include", "tarantool"))
}

func TestUninstallHeaderSelectionDoesNotSwitchExecutable(t *testing.T) {
	root := projectRoot(t)
	active := installVersion(t, root, binary.ProgramCe, "2.0.0")
	removed := installVersion(t, root, binary.ProgramCe, "3.0.0")
	installVersion(t, root, binary.ProgramCe, "4.0.0")
	linkInstallation(t, root, binary.ProgramCe, active)
	require.NoError(t, binary.CreateSymlink(filepath.Join(removed, "include", "tarantool"),
		filepath.Join(root, "include", "tarantool"), true))

	ctx := uninstallContext(binary.ScopeProject, binary.ProgramCe, "3.0.0")
	require.NoError(t, UninstallProgram(ctx))
	assertMissing(t, removed)
	assertMissing(t, filepath.Join(root, "include", "tarantool"))
	assertLink(t, filepath.Join(root, "bin", "tarantool"), filepath.Join(active, "tarantool"))
}
