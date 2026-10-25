package scope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/scope"
)

// writeTree creates the files under root, each with the given content, and
// the directories they need.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

const validManifest = "[package]\nname = \"app\"\nversion = \"1.0.0\"\n"

func TestFindProject(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		start string
		want  string // Relative to the tree; empty means no project.
	}{
		{
			name:  "manifest in start",
			files: map[string]string{"app/app.manifest.toml": validManifest},
			start: "app", want: "app",
		},
		{
			name:  "manifest one level up",
			files: map[string]string{"app/app.manifest.toml": validManifest},
			start: "app/src", want: "app",
		},
		{
			name:  "manifest three levels up",
			files: map[string]string{"app/app.manifest.toml": validManifest},
			start: "app/src/lib/deep", want: "app",
		},
		{
			name: "template in start",
			files: map[string]string{
				"app/.templates/default/instances.yaml": "storage-1:\n",
			},
			start: "app", want: "app",
		},
		{
			name: "template two levels up",
			files: map[string]string{
				"app/.templates/default/instances.yaml": "storage-1:\n",
			},
			start: "app/src/lib", want: "app",
		},
		{
			name: "any template name",
			files: map[string]string{
				"app/.templates/staging/config.yaml":       "",
				"app/.templates/production/config.yaml":    "",
				"app/.templates/production/instances.yaml": "router-1:\n",
			},
			start: "app/src", want: "app",
		},
		{
			name: "both markers",
			files: map[string]string{
				"app/app.manifest.toml":                 validManifest,
				"app/.templates/default/instances.yaml": "",
			},
			start: "app/src", want: "app",
		},
		{
			name: "nested project wins over the enclosing one",
			files: map[string]string{
				"outer/app.manifest.toml":                       validManifest,
				"outer/inner/.templates/default/instances.yaml": "",
			},
			start: "outer/inner/src", want: "outer/inner",
		},
		{
			name: "enclosing project above the nested one",
			files: map[string]string{
				"outer/app.manifest.toml":       validManifest,
				"outer/inner/app.manifest.toml": validManifest,
			},
			start: "outer/src", want: "outer",
		},
		{
			// The search does not read the manifest: a broken one still
			// marks the project, and the enclosing project is not reached.
			name: "broken manifest stops the search",
			files: map[string]string{
				"outer/app.manifest.toml":       validManifest,
				"outer/inner/app.manifest.toml": "[package\nname = ",
			},
			start: "outer/inner/src", want: "outer/inner",
		},
		{
			name: "empty manifest",
			files: map[string]string{
				"outer/app.manifest.toml":       validManifest,
				"outer/inner/app.manifest.toml": "",
			},
			start: "outer/inner", want: "outer/inner",
		},
		{
			name:  "no marker",
			files: map[string]string{"app/src/init.lua": ""},
			start: "app/src",
		},
		{
			name: "not markers",
			files: map[string]string{
				"app/config.yaml":                      "",
				"app/init.lua":                         "",
				"app/.rocks/tarantool/module.lua":      "",
				"app/.git/HEAD":                        "",
				"app/.tarantool/bin/tarantool":         "",
				"app/tt.yaml":                          "",
				"app/instances.yaml":                   "",
				"app/.templates/instances.yaml":        "",
				"app/.templates/default/instances.yml": "",
				"app/.templates/default/config.yaml":   "",
			},
			start: "app",
		},
		{
			name:  "templates is a file",
			files: map[string]string{"app/.templates": ""},
			start: "app",
		},
		{
			name:  "template is a file",
			files: map[string]string{"app/.templates/default": ""},
			start: "app",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tree := t.TempDir()
			writeTree(t, tree, testCase.files)

			start := filepath.Join(tree, testCase.start)
			require.NoError(t, os.MkdirAll(start, 0o755))

			dir, found, err := scope.FindProject(start)
			require.NoError(t, err)

			if testCase.want == "" {
				assert.False(t, found)
				assert.Empty(t, dir)
			} else {
				assert.True(t, found)
				assert.Equal(t, filepath.Join(tree, testCase.want), dir)
			}
		})
	}
}

func TestFindProjectMarkerOfAnyType(t *testing.T) {
	for name, create := range map[string]func(path string) error{
		"directory":        func(path string) error { return os.Mkdir(path, 0o755) },
		"dangling symlink": func(path string) error { return os.Symlink("missing", path) },
	} {
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			start := filepath.Join(project, "src")
			require.NoError(t, os.Mkdir(start, 0o755))
			require.NoError(t, create(filepath.Join(project, scope.ManifestFileName)))

			dir, found, err := scope.FindProject(start)
			require.NoError(t, err)
			assert.True(t, found)
			assert.Equal(t, project, dir)
		})
	}
}

func TestFindProjectTemplateSymlink(t *testing.T) {
	project := t.TempDir()
	shared := t.TempDir()
	writeTree(t, shared, map[string]string{"instances.yaml": ""})
	require.NoError(t, os.Mkdir(filepath.Join(project, ".templates"), 0o755))
	require.NoError(t, os.Symlink(shared, filepath.Join(project, ".templates", "default")))

	dir, found, err := scope.FindProject(project)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, project, dir)
}

func TestFindProjectStartDirectory(t *testing.T) {
	tree := t.TempDir()
	writeTree(t, tree, map[string]string{
		"cwd/app.manifest.toml":   validManifest,
		"other/app.manifest.toml": validManifest,
	})
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "other", "src"), 0o755))
	t.Chdir(filepath.Join(tree, "cwd"))

	// The search starts where the caller says, not in the working directory.
	dir, found, err := scope.FindProject(filepath.Join(tree, "other", "src"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, filepath.Join(tree, "other"), dir)

	// A relative start is resolved against the working directory.
	dir, found, err = scope.FindProject(filepath.Join("..", "other", "src"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, filepath.Join(tree, "other"), dir)
}

func TestFindProjectBadStart(t *testing.T) {
	tree := t.TempDir()
	writeTree(t, tree, map[string]string{
		"app.manifest.toml": validManifest,
		"file":              "",
	})

	// A start that is not a directory is reported, not replaced by the
	// project above it.
	_, found, err := scope.FindProject(filepath.Join(tree, "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.False(t, found)

	_, found, err = scope.FindProject(filepath.Join(tree, "file"))
	require.ErrorContains(t, err, "not a directory")
	assert.False(t, found)
}

func TestFindProjectUnreadableTemplates(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}

	tree := t.TempDir()
	writeTree(t, tree, map[string]string{
		"app.manifest.toml":                     validManifest,
		"nested/.templates/default/config.yaml": "",
	})

	templates := filepath.Join(tree, "nested", ".templates")
	require.NoError(t, os.Chmod(templates, 0o000))
	t.Cleanup(func() { _ = os.Chmod(templates, 0o755) })

	// Whether the nested directory is a project cannot be told: climbing to
	// the enclosing project instead would move the root.
	_, found, err := scope.FindProject(filepath.Join(tree, "nested"))
	require.ErrorIs(t, err, os.ErrPermission)
	require.ErrorContains(t, err, "cannot look for project templates")
	assert.False(t, found)
}

// installedVersion lays out an installed version of an application under
// root, with the manifest and template a project would have, and the apps/
// link to it. It returns the version directory and the link.
func installedVersion(t *testing.T, root string) (string, string) {
	t.Helper()

	version := filepath.Join(root, "versions", "myapp", "myapp_1.2.0")
	writeTree(t, version, map[string]string{
		"app.manifest.toml":                 validManifest,
		".templates/default/instances.yaml": "",
		"lib/init.lua":                      "",
	})

	link := filepath.Join(root, "apps", "myapp")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(filepath.Join("..", "versions", "myapp", "myapp_1.2.0"), link))

	return version, link
}

func TestFindProjectInstalledVersion(t *testing.T) {
	userRoot := filepath.Join(t.TempDir(), "user")
	systemRoot := filepath.Join(t.TempDir(), "system")
	t.Setenv(scope.UserRootEnv, userRoot)
	t.Setenv(scope.SystemRootEnv, systemRoot)

	for _, root := range []string{userRoot, systemRoot} {
		version, link := installedVersion(t, root)

		// Neither the version, nor a directory inside it, nor the version
		// reached through its apps/ link is a project, though each carries a
		// manifest and a template.
		for _, start := range []string{
			version, filepath.Join(version, "lib"), link, filepath.Join(link, "lib"),
		} {
			dir, found, err := scope.FindProject(start)
			require.NoError(t, err, start)
			assert.False(t, found, start)
			assert.Empty(t, dir, start)

			installed, err := scope.InstalledTree(start)
			require.NoError(t, err, start)
			assert.True(t, installed, start)
		}
	}

	// A project next to the roots is still a project.
	project := filepath.Join(t.TempDir(), "project")
	writeTree(t, project, map[string]string{"app.manifest.toml": validManifest})

	dir, found, err := scope.FindProject(project)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, project, dir)

	installed, err := scope.InstalledTree(project)
	require.NoError(t, err)
	assert.False(t, installed)
}

func TestFindProjectInstalledVersionLinkedRoot(t *testing.T) {
	// The root itself is reached through a link, as /opt -> /data/opt: the
	// version is recognised by its resolved path as well.
	target := filepath.Join(t.TempDir(), "target")
	linked := filepath.Join(t.TempDir(), "linked")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.Symlink(target, linked))
	t.Setenv(scope.UserRootEnv, linked)
	t.Setenv(scope.SystemRootEnv, filepath.Join(t.TempDir(), "system"))

	version, _ := installedVersion(t, target)

	_, found, err := scope.FindProject(version)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestFindProjectRelativeRoot(t *testing.T) {
	t.Setenv(scope.UserRootEnv, "relative")

	project := filepath.Join(t.TempDir(), "project")
	writeTree(t, project, map[string]string{"app.manifest.toml": validManifest})

	_, found, err := scope.FindProject(project)
	require.ErrorIs(t, err, scope.ErrRelativeRoot)
	assert.False(t, found)
}

func TestProjectRoot(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	assert.Equal(t, filepath.Join(project, ".tarantool"), scope.ProjectRoot(project))
}
