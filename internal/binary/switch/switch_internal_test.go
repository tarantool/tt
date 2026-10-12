package switcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

type installedFixture struct {
	Version     string
	BinaryPath  string
	HeadersPath string
}

func switchContext(scope binary.Scope, program binary.Program, version string) SwitchCtx {
	var ctx SwitchCtx

	ctx.Scope = scope
	ctx.Program = program
	ctx.Version = version

	return ctx
}

func installVersion(t *testing.T, root string, program binary.Program,
	version string,
) installedFixture {
	t.Helper()

	base := filepath.Join(root, binary.VersionsDirName, program.String(),
		program.String()+"_"+version)
	installed := installedFixture{
		Version:     version,
		BinaryPath:  filepath.Join(base, program.Exec()),
		HeadersPath: filepath.Join(base, binary.IncDirName, program.Exec()),
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(installed.BinaryPath), 0o755))
	require.NoError(t, os.WriteFile(installed.BinaryPath, []byte("binary"), 0o755))

	if program.IsTarantool() {
		require.NoError(t, os.MkdirAll(installed.HeadersPath, 0o755))
	}

	return installed
}

func assertLink(t *testing.T, path, target string) {
	t.Helper()

	actual, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	assert.Equal(t, target, actual)
}

func TestSwitchProducts(t *testing.T) {
	for _, program := range []binary.Program{
		binary.ProgramTt, binary.ProgramCe, binary.ProgramEe, binary.ProgramTcm,
	} {
		t.Run(program.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			installed := installVersion(t, root, program, "3.0.0")
			ctx := switchContext(binary.ScopeProject, program, installed.Version)

			require.NoError(t, Switch(&ctx))
			assertLink(t, filepath.Join(root, "bin", program.Exec()), installed.BinaryPath)

			if program.IsTarantool() {
				assertLink(t, filepath.Join(root, "inc", "tarantool"), installed.HeadersPath)
			} else {
				assert.NoDirExists(t, filepath.Join(root, "inc"))
			}
		})
	}
}

func TestSwitchTarantoolEditions(t *testing.T) {
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	community := installVersion(t, root, binary.ProgramCe, "3.0.0")
	enterprise := installVersion(t, root, binary.ProgramEe, "3.0.0")

	for _, program := range []binary.Program{binary.ProgramCe, binary.ProgramEe, binary.ProgramCe} {
		ctx := switchContext(binary.ScopeProject, program, "3.0.0")
		require.NoError(t, Switch(&ctx))

		expected := community
		if program == binary.ProgramEe {
			expected = enterprise
		}

		assertLink(t, filepath.Join(root, "bin", "tarantool"), expected.BinaryPath)
		assertLink(t, filepath.Join(root, "inc", "tarantool"), expected.HeadersPath)
	}
}

func TestSwitchRejectsIncompleteInstallationWithoutChanges(t *testing.T) {
	for _, missing := range []string{"binary", "headers", "headers_are_file"} {
		t.Run(missing, func(t *testing.T) {
			t.Chdir(t.TempDir())

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			active := installVersion(t, root, binary.ProgramCe, "2.10.3")
			ctx := switchContext(binary.ScopeProject, binary.ProgramCe, active.Version)
			require.NoError(t, Switch(&ctx))

			selected := installVersion(t, root, binary.ProgramEe, "3.0.0")

			switch missing {
			case "binary":
				require.NoError(t, os.Remove(selected.BinaryPath))
			case "headers":
				require.NoError(t, os.Remove(selected.HeadersPath))
			case "headers_are_file":
				require.NoError(t, os.Remove(selected.HeadersPath))
				require.NoError(t, os.WriteFile(selected.HeadersPath, nil, 0o644))
			}

			ctx.Program = binary.ProgramEe
			ctx.Version = selected.Version
			require.Error(t, Switch(&ctx))
			assertLink(t, filepath.Join(root, "bin", "tarantool"), active.BinaryPath)
			assertLink(t, filepath.Join(root, "inc", "tarantool"), active.HeadersPath)
		})
	}
}

func TestSwitchFromLocalBuild(t *testing.T) {
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	buildDir := filepath.Join(t.TempDir(), "versions", "tarantool", "tarantool_master")
	build := installedFixture{
		Version:     "master",
		BinaryPath:  filepath.Join(buildDir, "bin", "tarantool"),
		HeadersPath: filepath.Join(buildDir, "inc", "tarantool"),
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(build.BinaryPath), 0o755))
	require.NoError(t, os.WriteFile(build.BinaryPath, []byte("binary"), 0o755))
	require.NoError(t, os.MkdirAll(build.HeadersPath, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "inc"), 0o755))
	require.NoError(t, os.Symlink(build.BinaryPath, filepath.Join(root, "bin", "tarantool")))
	require.NoError(t, os.Symlink(build.HeadersPath, filepath.Join(root, "inc", "tarantool")))

	installed := installVersion(t, root, binary.ProgramEe, "3.0.0")
	ctx := switchContext(binary.ScopeProject, binary.ProgramEe, installed.Version)

	require.NoError(t, Switch(&ctx))
	assertLink(t, filepath.Join(root, "bin", "tarantool"), installed.BinaryPath)
	assertLink(t, filepath.Join(root, "inc", "tarantool"), installed.HeadersPath)
	assert.FileExists(t, build.BinaryPath)
	assert.DirExists(t, build.HeadersPath)
}

func TestSwitchVersionIdentity(t *testing.T) {
	testCases := []struct {
		name      string
		installed []string
		requested string
		expected  string
	}{
		{"short_release", []string{"v3.0.0"}, "3.0.0", "v3.0.0"},
		{"full_release", []string{"3.0.0"}, "v3.0.0", "3.0.0"},
		{"exact_short", []string{"3.0.0", "v3.0.0"}, "3.0.0", "3.0.0"},
		{"exact_full", []string{"3.0.0", "v3.0.0"}, "v3.0.0", "v3.0.0"},
		{"master", []string{"master", "vmaster"}, "master", "master"},
		{"sha", []string{"123abcd", "v123abcd"}, "123abcd", "123abcd"},
		{"missing", []string{"v3.0.0"}, "3.1.0", ""},
		{"no_master_alias", []string{"vmaster"}, "master", ""},
		{"no_sha_alias", []string{"v123abcd"}, "123abcd", ""},
		{"no_numeric_sha_alias", []string{"v1234567"}, "1234567", ""},
		{"no_double_prefix_alias", []string{"v3.0.0"}, "vv3.0.0", ""},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			root, err := binary.ScopeProject.ArtifactPath()
			require.NoError(t, err)

			for _, ver := range testCase.installed {
				installVersion(t, root, binary.ProgramTt, ver)
			}

			ctx := switchContext(binary.ScopeProject, binary.ProgramTt, testCase.requested)

			err = Switch(&ctx)
			assert.Equal(t, testCase.requested, ctx.Version)

			if testCase.expected == "" {
				require.Error(t, err)
				assert.NoDirExists(t, filepath.Join(root, "bin"))

				return
			}

			require.NoError(t, err)
			assertLink(t, filepath.Join(root, "bin", "tt"), filepath.Join(root,
				"versions", "tt", "tt_"+testCase.expected, "tt"))
		})
	}
}

func TestSwitchDoesNotFallBackToOtherScope(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(child, 0o755))
	t.Chdir(child)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	userRoot, err := binary.ScopeUser.ArtifactPath()
	require.NoError(t, err)
	installVersion(t, userRoot, binary.ProgramTt, "v3.0.0")
	installVersion(t, filepath.Join(parent, ".tarantool"), binary.ProgramTt, "v3.0.0")

	ctx := switchContext(binary.ScopeProject, binary.ProgramTt, "3.0.0")

	require.Error(t, Switch(&ctx))
	assert.NoDirExists(t, filepath.Join(child, ".tarantool"))
	assert.NoDirExists(t, filepath.Join(parent, ".tarantool", "bin"))
	assert.NoDirExists(t, filepath.Join(userRoot, "bin"))

	ctx.Scope = binary.ScopeUser
	require.NoError(t, Switch(&ctx))
	assertLink(t, filepath.Join(userRoot, "bin", "tt"), filepath.Join(userRoot,
		"versions", "tt", "tt_v3.0.0", "tt"))
	assert.NoDirExists(t, filepath.Join(child, ".tarantool"))

	installVersion(t, filepath.Join(child, ".tarantool"), binary.ProgramTt, "v4.0.0")

	ctx.Version = "4.0.0"
	require.Error(t, Switch(&ctx))
	assertLink(t, filepath.Join(userRoot, "bin", "tt"), filepath.Join(userRoot,
		"versions", "tt", "tt_v3.0.0", "tt"))
	assert.NoDirExists(t, filepath.Join(child, ".tarantool", "bin"))
}

func TestSwitchInvalidContext(t *testing.T) {
	testCases := []struct {
		program binary.Program
		scope   binary.Scope
		err     error
	}{
		{binary.ProgramUnknown, binary.ScopeProject, binary.ErrUnknownApplication},
		{binary.ProgramSDK, binary.ScopeProject, binary.ErrUnknownApplication},
		{binary.ProgramTt, binary.ScopeUnknown, binary.ErrUnknownScope},
	}
	for _, testCase := range testCases {
		ctx := switchContext(testCase.scope, testCase.program, "3.0.0")
		require.ErrorIs(t, Switch(&ctx), testCase.err)

		_, err := ChooseVersion(testCase.scope, testCase.program)
		require.ErrorIs(t, err, testCase.err)
	}
}

func TestChooseVersionEmptyScope(t *testing.T) {
	t.Chdir(t.TempDir())

	selected, err := ChooseVersion(binary.ScopeProject, binary.ProgramTt)
	require.ErrorContains(t, err, "there are no tt installed")
	assert.Empty(t, selected)
	assert.NoDirExists(t, ".tarantool")
}

func TestVersionChoices(t *testing.T) {
	versions := []version.Version{
		{Str: "v3.0.0 [active]"},
		{Str: "master"},
		{Str: "123abcd"},
	}
	expected := []string{
		binary.Bold(color.GreenString("3.0.0 [active]")),
		color.YellowString("master"),
		color.YellowString("123abcd"),
	}
	assert.Equal(t, expected, versionChoices(versions))
	assert.Equal(t, "v3.0.0 [active]", versions[0].Str)
}

func TestSwitchRepairsCyclicActiveLinks(t *testing.T) {
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	selected := installVersion(t, root, binary.ProgramCe, "3.0.0")
	for _, directory := range []string{"bin", "inc"} {
		link := filepath.Join(root, directory, "tarantool")
		require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
		require.NoError(t, os.Symlink("tarantool", link))
	}

	ctx := switchContext(binary.ScopeProject, binary.ProgramCe, "3.0.0")
	require.NoError(t, Switch(&ctx))
	assertLink(t, filepath.Join(root, "bin", "tarantool"), selected.BinaryPath)
	assertLink(t, filepath.Join(root, "inc", "tarantool"), selected.HeadersPath)
}

func TestSwitchIgnoresUnrelatedBrokenInstallation(t *testing.T) {
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	selected := installVersion(t, root, binary.ProgramTt, "v3.0.0")
	broken := installVersion(t, root, binary.ProgramTt, "v2.0.0")
	require.NoError(t, os.Remove(broken.BinaryPath))
	require.NoError(t, os.Symlink("tt", broken.BinaryPath))

	ctx := switchContext(binary.ScopeProject, binary.ProgramTt, "3.0.0")
	require.NoError(t, Switch(&ctx))
	assertLink(t, filepath.Join(root, "bin", "tt"), selected.BinaryPath)
}

func TestSwitchRejectsVersionTraversal(t *testing.T) {
	t.Chdir(t.TempDir())

	root, err := binary.ScopeProject.ArtifactPath()
	require.NoError(t, err)

	selected := installVersion(t, root, binary.ProgramTt, "v3.0.0")
	ctx := switchContext(binary.ScopeProject, binary.ProgramTt, selected.Version)
	require.NoError(t, Switch(&ctx))

	for _, requested := range []string{"", "..", "3.0.0/../../../tt_v3.0.0", "/tmp/tt_v3.0.0"} {
		ctx.Version = requested
		require.Error(t, Switch(&ctx))
		assertLink(t, filepath.Join(root, "bin", "tt"), selected.BinaryPath)
	}
}
