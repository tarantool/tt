package cargo

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// member is the Cargo.toml of the package mod1, a member of a workspace
// unless it says otherwise.
const member = "[package]\nname = \"mod1\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"

// TestExportOfAWorkspaceOutOfTheProject: the export of a package whose
// Cargo.toml, or the root of whose workspace - the Cargo.toml and the
// Cargo.lock cargo builds it with - is out of the project, above it, fails
// with build.ErrOutside, as the source rock holds neither; one whose root is
// in the project, of its own or of a workspace in it, exports. The root is
// found as cargo finds it, which the real cargo confirms when it is there.
func TestExportOfAWorkspaceOutOfTheProject(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		// files are written into the directory of the test, ws/app being
		// the project and ws/app/mod1 the working directory of the build.
		files map[string]string
		// root is the root of the workspace, relative to the project, and
		// want what the export says of it; empty for none.
		root, want string
	}{
		{
			name: "a workspace above the project",
			files: map[string]string{
				"ws/Cargo.toml":          "[workspace]\nmembers = [\"app/mod1\"]\n",
				"ws/app/mod1/Cargo.toml": member,
			},
			root: "..",
			want: "the build reads files out of the project: the cargo package in mod1 is of " +
				"the workspace of .., whose Cargo.toml and Cargo.lock cargo builds it with",
		},
		{
			name: "a workspace above the project of a table of its own",
			files: map[string]string{
				"ws/Cargo.toml": "[package]\nname = \"top\"\nversion = \"0.1.0\"\n\n" +
					"[workspace.dependencies] # shared\nserde = \"1\"\n\n[workspace]\n" +
					"members = [\n  \"app/mod1\", # the module\n]\n",
				"ws/src/lib.rs":          "",
				"ws/app/mod1/Cargo.toml": member,
			},
			root: "..",
			want: "the build reads files out of the project: the cargo package in mod1 is of " +
				"the workspace of .., whose Cargo.toml and Cargo.lock cargo builds it with",
		},
		{
			name: "a workspace package.workspace names above the project",
			files: map[string]string{
				"ws/Cargo.toml": "[workspace]\nmembers = [\"app/mod1\"]\n",
				"ws/app/mod1/Cargo.toml": "# [workspace]\n" + member +
					"workspace = '../..' # the root\n",
			},
			root: "..",
			want: "the build reads files out of the project: the cargo package in mod1 is of " +
				"the workspace of .., whose Cargo.toml and Cargo.lock cargo builds it with",
		},
		{
			name: "a workspace in the project",
			files: map[string]string{
				"ws/Cargo.toml":          "[workspace]\nmembers = []\n",
				"ws/app/Cargo.toml":      "[workspace] # of the project\nmembers = [\"mod1\"]\n",
				"ws/app/mod1/Cargo.toml": member,
			},
			root: ".",
		},
		{
			name: "a package of its own workspace",
			files: map[string]string{
				"ws/Cargo.toml":          "[workspace]\nmembers = [\"app/mod1\"]\n",
				"ws/app/mod1/Cargo.toml": member + "\n[workspace]\n",
			},
			root: "mod1",
		},
		{
			name: "a package the workspace above excludes",
			files: map[string]string{
				"ws/Cargo.toml": "[workspace]\nmembers = []\nexclude = [\n  'other',\n" +
					"  \"app\", # the project\n]\n",
				"ws/app/mod1/Cargo.toml": member,
			},
			root: "mod1",
		},
		{
			name: "a package of no workspace",
			files: map[string]string{
				"ws/Cargo.toml":          "[package]\nname = \"top\"\nversion = \"0.1.0\"\n",
				"ws/src/lib.rs":          "",
				"ws/app/mod1/Cargo.toml": member,
			},
			root: "mod1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFiles(t, dir, test.files)
			writeFiles(t, dir, map[string]string{"ws/app/mod1/src/lib.rs": ""})

			project := filepath.Join(dir, "ws", "app")
			cwd := filepath.Join(project, "mod1")

			root, err := findRoot(cwd)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(project, filepath.FromSlash(test.root)), root)

			requireRootOfCargo(t, cwd, root)

			bld := cargoBuild{config: Config{}, bin: "", module: true, static: false}

			_, err = bld.Export(&build.ExportContext{Project: project, Cwd: cwd})
			if test.want == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, build.ErrOutside)
			require.EqualError(t, err, test.want)

			// A core that says no directories gets the script.
			_, err = bld.Export(&build.ExportContext{})
			require.NoError(t, err)
		})
	}
}

// TestExportOfAPackageOutOfTheProject: a working directory of no Cargo.toml
// of its own, whose nearest one is out of the project, fails the export too.
func TestExportOfAPackageOutOfTheProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Cargo.toml": member, "app/mod1/src/lib.rs": ""})

	project := filepath.Join(dir, "app")

	_, err := cargoBuild{config: Config{}, bin: "", module: true, static: false}.Export(
		&build.ExportContext{Project: project, Cwd: filepath.Join(project, "mod1")})
	require.ErrorIs(t, err, build.ErrOutside)
	require.EqualError(t, err, "the build reads files out of the project: the Cargo.toml of "+
		"the cargo package, ../Cargo.toml, is out of the project")
}

// requireRootOfCargo checks that the real cargo, when it is in PATH, names
// root as the root of the workspace of the package in cwd.
func requireRootOfCargo(t *testing.T, cwd, root string) {
	t.Helper()

	_, err := exec.LookPath(cargoTool)
	if err != nil {
		return
	}

	cmd := exec.CommandContext(t.Context(), cargoTool, "locate-project", "--workspace",
		"--message-format", "plain")

	cmd.Dir = cwd

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, filepath.Join(root, manifestName), strings.TrimSpace(string(out)))
}
