package create_test

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/create"
	"github.com/tarantool/tt/v3/cli/create/builtin_templates"
	create_ctx "github.com/tarantool/tt/v3/cli/create/context"
)

// update rewrites the golden files of this package instead of comparing
// against them: go test ./cli/create -run TestBuiltinTemplates -update.
var update = flag.Bool("update", false, "rewrite golden files")

// builtinInstances are the instances every built-in template runs with the
// default values of its variables, in the order they start in: the
// description of every one is "*", the whole topology of its config, in the
// order of the names.
func builtinInstances() map[string][]string {
	return map[string][]string{
		"single_instance": {"instance001"},
		"cluster": {
			"replicaset-001-a", "replicaset-001-b",
			"replicaset-002-a", "replicaset-002-b",
		},
		"config_storage": {"replicaset-001-a", "replicaset-001-b", "replicaset-001-c"},
		"vshard_cluster": {
			"router-001-a",
			"storage-001-a", "storage-001-b",
			"storage-002-a", "storage-002-b",
		},
	}
}

// TestBuiltinTemplates checks what every built-in template creates with the
// default values of its variables: a project without tt.yaml, whose tree is
// pinned by a golden file, and which the instance commands load as it is -
// named after --name, running from the complete pair in .templates/default/
// with the instances the template describes. Regenerate the golden files
// with -update and review the diff.
func TestBuiltinTemplates(t *testing.T) {
	expected := builtinInstances()

	for _, name := range builtin_templates.Names {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, expected, name, "a new built-in template needs its instances here")

			dst := t.TempDir()
			createCtx := create_ctx.CreateCtx{
				AppName:        "app1",
				WorkDir:        dst,
				DestinationDir: dst,
				TemplateName:   name,
				SilentMode:     true,
			}
			require.NoError(t, create.Run(&createCtx))

			project := filepath.Join(dst, "app1")
			assert.NoFileExists(t, filepath.Join(project, "tt.yaml"))

			compareGolden(t, filepath.Join("testdata", name+".golden"), renderTree(t, project))

			loaded, err := app.Load(t.Context(), "", app.Options{Start: project})
			require.NoError(t, err)
			assert.Equal(t, "app1", loaded.Name)
			assert.Equal(t, app.ScopeProject, loaded.Scope)
			assert.Equal(t, project, loaded.Dir)
			assert.Equal(t, app.DefaultTemplate, loaded.Pair.Template)
			assert.Equal(t, filepath.Join(project, ".templates", "default"), loaded.Pair.Dir)

			names := make([]string, 0, len(loaded.Instances))
			for _, inst := range loaded.Instances {
				names = append(names, inst.Name)
			}

			assert.Equal(t, expected[name], names)
		})
	}
}

// TestBuiltinTemplatePackageName checks --name against the package name a
// built-in template writes to its project manifest: vshard_cluster replaces
// "_" with "-", so my_app is a project called my-app, while My_App is refused
// before anything is created. A template without a manifest takes any name.
func TestBuiltinTemplatePackageName(t *testing.T) {
	createApp := func(t *testing.T, template, name string) (string, error) {
		t.Helper()

		dst := t.TempDir()
		createCtx := create_ctx.CreateCtx{
			AppName:        name,
			WorkDir:        dst,
			DestinationDir: dst,
			TemplateName:   template,
			SilentMode:     true,
		}

		return filepath.Join(dst, name), create.Run(&createCtx)
	}

	t.Run("vshard_cluster my_app", func(t *testing.T) {
		project, err := createApp(t, "vshard_cluster", "my_app")
		require.NoError(t, err)

		loaded, err := app.Load(t.Context(), "", app.Options{Start: project})
		require.NoError(t, err)
		assert.Equal(t, "my-app", loaded.Name)
	})

	t.Run("vshard_cluster My_App", func(t *testing.T) {
		project, err := createApp(t, "vshard_cluster", "My_App")
		require.EqualError(t, err, `application name "My_App" gives package.name "My-App" `+
			`in app.manifest.toml, which is not a package name: use lowercase letters, `+
			`digits and dashes, starting with a letter, and not a reserved name`)
		assert.NoDirExists(t, project)
	})

	t.Run("cluster My_App", func(t *testing.T) {
		project, err := createApp(t, "cluster", "My_App")
		require.NoError(t, err)

		loaded, err := app.Load(t.Context(), "", app.Options{Start: project})
		require.NoError(t, err)
		assert.Equal(t, "My_App", loaded.Name)
	})
}

// renderTree renders the regular files under dir, hidden ones included, in
// lexical order, each as "==> <path relative to dir> <==" and its content.
func renderTree(t *testing.T, dir string) string {
	t.Helper()

	var tree strings.Builder

	root := os.DirFS(dir)

	err := fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}

		content, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}

		tree.WriteString("==> " + path + " <==\n")
		tree.Write(content)

		if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
			tree.WriteString("\n")
		}

		return nil
	})
	require.NoError(t, err)

	return tree.String()
}

// compareGolden compares got with the golden file path, or rewrites the file
// with got under -update.
func compareGolden(t *testing.T, path, got string) {
	t.Helper()

	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))

		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "no golden file; run the test with -update")
	assert.Equal(t, string(want), got)
}
