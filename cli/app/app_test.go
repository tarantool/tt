package app_test

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/scope"
)

// manifestOf returns an app.manifest.toml naming the package name.
func manifestOf(name string) string {
	return fmt.Sprintf("manifest_version = \"0.1\"\n\n[package]\nname = %q\n", name)
}

// topology returns a cluster config declaring the instances names, one
// replicaset of one group.
func topology(names ...string) string {
	var config strings.Builder

	config.WriteString("groups:\n  g:\n    replicasets:\n      r:\n        instances:\n")

	for _, name := range names {
		fmt.Fprintf(&config, "          %s: {}\n", name)
	}

	return config.String()
}

// description returns an instances description declaring the names as
// Tarantool instances configured by the cluster config.
func description(names ...string) string {
	var desc strings.Builder

	for _, name := range names {
		desc.WriteString(name + ":\n")
	}

	return desc.String()
}

// templateFiles returns the files of the template named template under dir,
// a pair declaring the instances names in both files.
func templateFiles(dir, template string, names ...string) map[string]string {
	pairDir := filepath.Join(dir, scope.TemplatesDirName, template)

	return map[string]string{
		filepath.Join(pairDir, scope.InstancesFileName): description(names...),
		filepath.Join(pairDir, "config.yaml"):           topology(names...),
	}
}

// merge returns the files of every tree in one tree; a later tree wins.
func merge(trees ...map[string]string) map[string]string {
	files := map[string]string{}
	for _, tree := range trees {
		maps.Copy(files, tree)
	}

	return files
}

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

// roots points the user and the system scopes at fresh temporary
// directories, so that no test reads the roots of the machine.
func roots(t *testing.T) (string, string) {
	t.Helper()

	user := t.TempDir()
	system := t.TempDir()

	t.Setenv(scope.UserRootEnv, user)
	t.Setenv(scope.SystemRootEnv, system)

	return user, system
}

// install lays an application down under root the way the user and system
// scopes keep it: files go to versions/<name>/<name>_<version>/, and
// apps/<name> becomes a relative link to that directory, replacing the link
// to an earlier version. It returns the version directory.
func install(t *testing.T, root, name, version string, files map[string]string) string {
	t.Helper()

	rel := filepath.Join("versions", name, name+"_"+version)
	versionDir := filepath.Join(root, rel)

	require.NoError(t, os.MkdirAll(versionDir, 0o755))
	writeTree(t, versionDir, files)

	link := filepath.Join(root, "apps", name)
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))

	_ = os.Remove(link)

	require.NoError(t, os.Symlink(filepath.Join("..", rel), link))

	return versionDir
}

// names returns the names of records.
func names(records []instances.Instance) []string {
	list := make([]string, 0, len(records))
	for _, record := range records {
		list = append(list, record.Name)
	}

	return list
}

// load calls app.Load with a context without a repository.
func load(t *testing.T, name string, opts app.Options) (*app.App, error) {
	t.Helper()

	return app.Load(context.Background(), name, opts)
}

// requireFailure checks that err wraps want and carries sdk.ExitFailure.
func requireFailure(t *testing.T, err, want error) {
	t.Helper()

	require.ErrorIs(t, err, want)
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
}

func TestLoadProject(t *testing.T) {
	roots(t)

	tree := t.TempDir()
	projectDir := filepath.Join(tree, "work")
	writeTree(t, tree, merge(templateFiles("work", "default", "router-1", "storage-1"),
		map[string]string{
			"work/app.manifest.toml": manifestOf("myapp"),
			"work/src/lib/init.lua":  "",
		}))

	for _, start := range []string{projectDir, filepath.Join(projectDir, "src", "lib")} {
		got, err := load(t, "", app.Options{Start: start})
		require.NoError(t, err)

		assert.Equal(t, "myapp", got.Name, "named by the manifest, not the directory")
		assert.Equal(t, app.ScopeProject, got.Scope)
		assert.Equal(t, filepath.Join(projectDir, ".tarantool"), got.Root)
		assert.Equal(t, projectDir, got.Dir)
		assert.Equal(t, app.Pair{
			Template:      "default",
			Dir:           filepath.Join(projectDir, ".templates", "default"),
			InstancesFile: filepath.Join(projectDir, ".templates", "default", "instances.yaml"),
			ConfigFile:    filepath.Join(projectDir, ".templates", "default", "config.yaml"),
		}, got.Pair)
		assert.False(t, got.Pair.Host())
		assert.Equal(t, []string{"router-1", "storage-1"}, names(got.Instances))
		require.NotNil(t, got.Config)
	}
}

func TestLoadProjectWithoutManifest(t *testing.T) {
	roots(t)

	tree := t.TempDir()
	writeTree(t, tree, templateFiles("my-project", "default", "storage-1"))

	got, err := load(t, "", app.Options{Start: filepath.Join(tree, "my-project")})
	require.NoError(t, err)
	assert.Equal(t, "my-project", got.Name)
	assert.Equal(t, filepath.Join(tree, "my-project"), got.Dir)
}

func TestLoadProjectBrokenManifest(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
	}{
		{name: "not TOML", manifest: "[package\nname = \"myapp\"\n"},
		{name: "unknown field", manifest: manifestOf("myapp") + "colour = \"red\"\n"},
		{name: "no name", manifest: "manifest_version = \"0.1\"\n\n[package]\n"},
		{name: "unusable name", manifest: manifestOf("My App")},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, system := roots(t)

			tree := t.TempDir()
			writeTree(t, tree, merge(templateFiles("myapp", "default", "storage-1"),
				map[string]string{"myapp/app.manifest.toml": test.manifest}))

			// Even with myapp installed in the system scope: a project that
			// cannot tell its name might be the application meant.
			install(t, system, "myapp", "1.0.0", templateFiles(".", "default", "storage-1"))

			for _, name := range []string{"", "myapp"} {
				_, err := load(t, name, app.Options{Start: filepath.Join(tree, "myapp")})
				requireFailure(t, err, app.ErrInvalidManifest)
				require.ErrorContains(t, err, "app.manifest.toml")
			}
		})
	}
}

func TestLoadInstalled(t *testing.T) {
	for _, scp := range []app.Scope{app.ScopeUser, app.ScopeSystem} {
		t.Run(string(scp), func(t *testing.T) {
			user, system := roots(t)

			root := system
			if scp == app.ScopeUser {
				root = user
			}

			install(t, root, "myapp", "1.0.0", templateFiles(".", "default", "storage-1"))

			version := install(t, root, "myapp", "1.2.0",
				templateFiles(".", "default", "storage-1", "storage-2"))

			got, err := load(t, "myapp", app.Options{Scope: scp, Start: t.TempDir()})
			require.NoError(t, err)

			wantDir, err := filepath.EvalSymlinks(version)
			require.NoError(t, err)

			assert.Equal(t, "myapp", got.Name)
			assert.Equal(t, scp, got.Scope)
			assert.Equal(t, root, got.Root)
			assert.Equal(t, wantDir, got.Dir, "the version, not the link")
			assert.Equal(t, filepath.Join(wantDir, ".templates", "default"), got.Pair.Dir)
			assert.Equal(t, []string{"storage-1", "storage-2"}, names(got.Instances))
		})
	}
}

func TestLoadInstalledPathsPointIntoTheVersion(t *testing.T) {
	_, system := roots(t)

	version := install(t, system, "myapp", "1.0.0", merge(
		templateFiles(".", "default", "storage-1"),
		map[string]string{
			".templates/default/instances.yaml": "storage-1:\nworker-1:\n  binary: ./bin/worker\n",
		}))

	got, err := load(t, "myapp", app.Options{Start: t.TempDir()})
	require.NoError(t, err)

	wantDir, err := filepath.EvalSymlinks(version)
	require.NoError(t, err)

	require.Len(t, got.Instances, 2)
	assert.Equal(t, filepath.Join(wantDir, "bin", "worker"), got.Instances[1].Binary)
}

func TestLoadInstalledMissing(t *testing.T) {
	cases := []struct {
		name  string
		scope app.Scope
		msg   string
	}{
		{name: "explicit system", scope: app.ScopeSystem, msg: "in the system scope"},
		{name: "explicit user", scope: app.ScopeUser, msg: "in the user scope"},
		{name: "system by default", scope: "", msg: "in the system scope"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			user, _ := roots(t)

			// Installed in the user scope, which is never searched unasked.
			install(t, user, "other", "1.0.0", templateFiles(".", "default", "storage-1"))

			_, err := load(t, "myapp", app.Options{Scope: test.scope, Start: t.TempDir()})
			requireFailure(t, err, app.ErrAppNotFound)
			require.ErrorContains(t, err, test.msg)
			require.ErrorContains(t, err, "--scope")
		})
	}

	t.Run("not in the user scope unless asked", func(t *testing.T) {
		user, _ := roots(t)
		install(t, user, "myapp", "1.0.0", templateFiles(".", "default", "storage-1"))

		_, err := load(t, "myapp", app.Options{Start: t.TempDir()})
		requireFailure(t, err, app.ErrAppNotFound)
		require.ErrorContains(t, err, "in the system scope")
	})

	t.Run("a dangling link", func(t *testing.T) {
		_, system := roots(t)
		require.NoError(t, os.MkdirAll(filepath.Join(system, "apps"), 0o755))
		require.NoError(t, os.Symlink("../versions/myapp/myapp_1.0.0",
			filepath.Join(system, "apps", "myapp")))

		_, err := load(t, "myapp", app.Options{Start: t.TempDir()})
		requireFailure(t, err, app.ErrAppNotFound)
	})
}

// TestLoadPinnedVersion pins that a pinned directory keeps an installed
// application on the version it names after apps/<app> has moved on, while
// its pair is read anew from that version.
func TestLoadPinnedVersion(t *testing.T) {
	for _, scp := range []app.Scope{app.ScopeUser, app.ScopeSystem} {
		t.Run(string(scp), func(t *testing.T) {
			user, system := roots(t)

			root := system
			if scp == app.ScopeUser {
				root = user
			}

			first, err := filepath.EvalSymlinks(install(t, root, "myapp", "1.0.0",
				templateFiles(".", "default", "storage-1")))
			require.NoError(t, err)

			install(t, root, "myapp", "2.0.0",
				templateFiles(".", "default", "storage-1", "storage-2"))

			// The pair of the pinned version changes after it was first read.
			writeTree(t, first, templateFiles(".", "default", "storage-1", "storage-3"))

			got, err := load(t, "myapp", app.Options{Scope: scp, Dir: first, Start: t.TempDir()})
			require.NoError(t, err)

			assert.Equal(t, "myapp", got.Name)
			assert.Equal(t, scp, got.Scope)
			assert.Equal(t, root, got.Root)
			assert.Equal(t, first, got.Dir)
			assert.Equal(t, []string{"storage-1", "storage-3"}, names(got.Instances))

			active, err := load(t, "myapp", app.Options{Scope: scp, Start: t.TempDir()})
			require.NoError(t, err)
			assert.Equal(t, []string{"storage-1", "storage-2"}, names(active.Instances),
				"without a pin the active version is loaded")
		})
	}
}

// TestLoadPinnedHostPair pins that a pinned version still runs from the pair
// of the host when the host has one: the pin chooses the version, not the
// pair.
func TestLoadPinnedHostPair(t *testing.T) {
	_, system := roots(t)

	version := install(t, system, "myapp", "1.0.0", templateFiles(".", "default", "storage-1"))
	writeTree(t, filepath.Join(system, "etc", "myapp"), map[string]string{
		"instances.yaml": description("storage-2"),
		"config.yaml":    topology("storage-1", "storage-2"),
	})

	got, err := load(t, "myapp", app.Options{Scope: app.ScopeSystem, Dir: version})
	require.NoError(t, err)
	assert.True(t, got.Pair.Host())
	assert.Equal(t, []string{"storage-2"}, names(got.Instances))
}

// TestLoadPinnedProject pins that a pinned project is loaded from its
// directory whatever the start directory, and is still named by its
// manifest.
func TestLoadPinnedProject(t *testing.T) {
	roots(t)

	tree := t.TempDir()
	projectDir := filepath.Join(tree, "work")
	writeTree(t, tree, merge(templateFiles("work", "default", "storage-1"),
		map[string]string{"work/app.manifest.toml": manifestOf("myapp")}))

	outside := t.TempDir()

	for _, name := range []string{"", "myapp"} {
		got, err := load(t, name, app.Options{
			Scope: app.ScopeProject, Dir: projectDir, Start: outside,
		})
		require.NoError(t, err)

		assert.Equal(t, "myapp", got.Name)
		assert.Equal(t, app.ScopeProject, got.Scope)
		assert.Equal(t, filepath.Join(projectDir, ".tarantool"), got.Root)
		assert.Equal(t, projectDir, got.Dir)
	}

	_, err := load(t, "other", app.Options{Scope: app.ScopeProject, Dir: projectDir})
	requireFailure(t, err, app.ErrAppNotFound)
	require.ErrorContains(t, err, `called "myapp", not "other"`)
}

func TestLoadPinnedErrors(t *testing.T) {
	_, system := roots(t)

	version := install(t, system, "myapp", "1.0.0", templateFiles(".", "default", "storage-1"))

	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))

	cases := []struct {
		name   string
		target string
		opts   app.Options
		want   error
		msg    string
	}{
		{
			name: "relative", target: "myapp",
			opts: app.Options{Scope: app.ScopeSystem, Dir: "versions/myapp"},
			want: nil, msg: "not an absolute path",
		},
		{
			name: "gone", target: "myapp",
			opts: app.Options{Scope: app.ScopeSystem, Dir: filepath.Join(system, "nowhere")},
			want: app.ErrAppNotFound, msg: "is gone",
		},
		{
			name: "a file", target: "myapp",
			opts: app.Options{Scope: app.ScopeSystem, Dir: file},
			want: app.ErrAppNotFound, msg: "not a directory",
		},
		{
			name: "no scope", target: "myapp",
			opts: app.Options{Dir: version},
			want: nil, msg: "pinned without its scope",
		},
		{
			name: "no name", target: "",
			opts: app.Options{Scope: app.ScopeSystem, Dir: version},
			want: app.ErrInvalidTarget, msg: "needs a target",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(t, test.target, test.opts)
			require.Error(t, err)
			assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
			require.ErrorContains(t, err, test.msg)

			if test.want != nil {
				require.ErrorIs(t, err, test.want)
			}
		})
	}
}

func TestScopeSelection(t *testing.T) {
	user, system := roots(t)

	tree := t.TempDir()
	writeTree(t, tree, merge(
		templateFiles("work", "default", "local-1"),
		templateFiles("other", "default", "local-1"),
		map[string]string{
			"work/app.manifest.toml":  manifestOf("myapp"),
			"other/app.manifest.toml": manifestOf("other"),
			"other/sub/.keep":         "",
		}))
	install(t, system, "myapp", "1.0.0", templateFiles(".", "default", "system-1"))
	install(t, user, "myapp", "1.0.0", templateFiles(".", "default", "user-1"))

	cases := []struct {
		name  string
		start string
		scope app.Scope
		want  app.Scope
		inst  string
	}{
		{
			name: "inside the project called so", start: "work/src",
			want: app.ScopeProject, inst: "local-1",
		},
		{
			name: "inside a project called otherwise", start: "other/sub",
			want: app.ScopeSystem, inst: "system-1",
		},
		{
			name: "outside of any project", start: ".",
			want: app.ScopeSystem, inst: "system-1",
		},
		{
			name: "explicit user wins over the project", start: "work",
			scope: app.ScopeUser, want: app.ScopeUser, inst: "user-1",
		},
		{
			name: "explicit system wins over the project", start: "work",
			scope: app.ScopeSystem, want: app.ScopeSystem, inst: "system-1",
		},
		{
			name: "explicit project", start: "work",
			scope: app.ScopeProject, want: app.ScopeProject, inst: "local-1",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, os.MkdirAll(filepath.Join(tree, test.start), 0o755))

			got, err := load(t, "myapp", app.Options{
				Scope: test.scope,
				Start: filepath.Join(tree, test.start),
			})
			require.NoError(t, err)
			assert.Equal(t, test.want, got.Scope)
			assert.Equal(t, []string{test.inst}, names(got.Instances))
		})
	}
}

func TestScopeSelectionErrors(t *testing.T) {
	_, system := roots(t)

	tree := t.TempDir()
	writeTree(t, tree, merge(templateFiles("other", "default", "storage-1"),
		map[string]string{
			"other/app.manifest.toml": manifestOf("other"),
			"outside/.keep":           "",
		}))
	install(t, system, "myapp", "1.0.0", templateFiles(".", "default", "storage-1"))

	t.Run("explicit project called otherwise", func(t *testing.T) {
		_, err := load(t, "myapp", app.Options{
			Scope: app.ScopeProject,
			Start: filepath.Join(tree, "other"),
		})
		requireFailure(t, err, app.ErrAppNotFound)
		require.ErrorContains(t, err, `is called "other", not "myapp"`)
	})

	t.Run("explicit project outside of any project", func(t *testing.T) {
		_, err := load(t, "myapp", app.Options{
			Scope: app.ScopeProject,
			Start: filepath.Join(tree, "outside"),
		})
		requireFailure(t, err, app.ErrNoProject)
	})

	t.Run("no target outside of any project", func(t *testing.T) {
		_, err := load(t, "", app.Options{Start: filepath.Join(tree, "outside")})
		requireFailure(t, err, app.ErrNoProject)
		require.ErrorContains(t, err, "use <APP>:<INSTANCE> or <APP>:*")
		require.NotErrorIs(t, err, app.ErrTT2Environment)
	})

	t.Run("no target with an installed scope", func(t *testing.T) {
		for _, scp := range []app.Scope{app.ScopeUser, app.ScopeSystem} {
			_, err := load(t, "", app.Options{Scope: scp, Start: filepath.Join(tree, "other")})
			requireFailure(t, err, app.ErrInvalidTarget)
			require.ErrorContains(t, err, "--scope "+string(scp)+" needs a target")
		}
	})

	t.Run("system scope missing names the project", func(t *testing.T) {
		_, err := load(t, "nope", app.Options{Start: filepath.Join(tree, "other")})
		requireFailure(t, err, app.ErrAppNotFound)
		require.ErrorContains(t, err, `the project in `+filepath.Join(tree, "other")+
			` is called "other"`)
	})

	t.Run("unknown scope", func(t *testing.T) {
		_, err := load(t, "myapp", app.Options{Scope: "global", Start: tree})
		requireFailure(t, err, app.ErrUnknownScope)
	})

	t.Run("invalid name", func(t *testing.T) {
		_, err := load(t, "../myapp", app.Options{Start: tree})
		requireFailure(t, err, app.ErrInvalidTarget)
	})
}

func TestTT2Environment(t *testing.T) {
	for _, file := range []string{"tt.yaml", "tt.yml", "instances.yml"} {
		t.Run(file, func(t *testing.T) {
			roots(t)

			tree := t.TempDir()
			// Unreadable content: the file is only looked for.
			writeTree(t, tree, map[string]string{
				"env/" + file:       "{{{ not yaml",
				"env/app/init.lua":  "",
				"env/app/.keep":     "",
				"elsewhere/.keep":   "",
				"env/config.yaml":   topology("storage-1"),
				"env/app/sub/.keep": "",
			})

			for _, start := range []string{"env", "env/app/sub"} {
				_, err := load(t, "", app.Options{Start: filepath.Join(tree, start)})
				requireFailure(t, err, app.ErrNoProject)
				require.ErrorIs(t, err, app.ErrTT2Environment)
				require.ErrorContains(t, err, filepath.Join(tree, "env", file))
				require.ErrorContains(t, err, "doc/migration_from_older_versions.md")

				_, err = load(t, "myapp", app.Options{Start: filepath.Join(tree, start)})
				requireFailure(t, err, app.ErrAppNotFound)
				require.ErrorIs(t, err, app.ErrTT2Environment)
			}

			_, err := load(t, "", app.Options{Start: filepath.Join(tree, "elsewhere")})
			requireFailure(t, err, app.ErrNoProject)
			require.NotErrorIs(t, err, app.ErrTT2Environment)
		})
	}

	t.Run("a project is no tt2 environment", func(t *testing.T) {
		roots(t)

		tree := t.TempDir()
		writeTree(t, tree, merge(templateFiles(".", "default", "storage-1"),
			map[string]string{"tt.yaml": "app:\n  run_dir: var/run\n"}))

		got, err := load(t, "", app.Options{Start: tree})
		require.NoError(t, err)
		assert.Equal(t, filepath.Base(tree), got.Name)
	})

	t.Run("instances.yaml of a host pair is no tt2 file", func(t *testing.T) {
		_, system := roots(t)

		hostDir := filepath.Join(system, "etc", "myapp")
		writeTree(t, hostDir, map[string]string{"instances.yaml": "storage-1:\n"})

		_, err := load(t, "myapp", app.Options{Start: hostDir})
		requireFailure(t, err, app.ErrAppNotFound)
		require.NotErrorIs(t, err, app.ErrTT2Environment)
	})
}

func TestPairSelection(t *testing.T) {
	_, system := roots(t)

	version := install(t, system, "myapp", "1.0.0", merge(
		templateFiles(".", "default", "storage-1"),
		templateFiles(".", "staging", "storage-1", "storage-2")))

	versionDir, err := filepath.EvalSymlinks(version)
	require.NoError(t, err)

	start := t.TempDir()

	t.Run("the default template without a host pair", func(t *testing.T) {
		got, err := load(t, "myapp", app.Options{Start: start})
		require.NoError(t, err)
		assert.Equal(t, "default", got.Pair.Template)
		assert.Equal(t, filepath.Join(versionDir, ".templates", "default"), got.Pair.Dir)
	})

	t.Run("--template", func(t *testing.T) {
		got, err := load(t, "myapp", app.Options{Template: "staging", Start: start})
		require.NoError(t, err)
		assert.Equal(t, "staging", got.Pair.Template)
		assert.Equal(t, []string{"storage-1", "storage-2"}, names(got.Instances))
	})

	t.Run("an empty host directory leaves the template", func(t *testing.T) {
		hostDir := filepath.Join(system, "etc", "myapp")
		require.NoError(t, os.MkdirAll(hostDir, 0o755))
		t.Cleanup(func() { _ = os.RemoveAll(hostDir) })

		got, err := load(t, "myapp", app.Options{Start: start})
		require.NoError(t, err)
		assert.Equal(t, "default", got.Pair.Template)
	})

	t.Run("the host pair wins", func(t *testing.T) {
		hostDir := filepath.Join(system, "etc", "myapp")
		writeTree(t, hostDir, map[string]string{
			"instances.yaml": "storage-2:\n",
			"config.yaml":    topology("storage-1", "storage-2"),
		})
		t.Cleanup(func() { _ = os.RemoveAll(hostDir) })

		got, err := load(t, "myapp", app.Options{Start: start})
		require.NoError(t, err)
		assert.True(t, got.Pair.Host())
		assert.Equal(t, app.Pair{
			Template:      "",
			Dir:           hostDir,
			InstancesFile: filepath.Join(hostDir, "instances.yaml"),
			ConfigFile:    filepath.Join(hostDir, "config.yaml"),
		}, got.Pair)
		assert.Equal(t, []string{"storage-2"}, names(got.Instances))
		assert.Equal(t, versionDir, got.Dir, "the application stays the version")

		_, err = load(t, "myapp", app.Options{Template: "staging", Start: start})
		requireFailure(t, err, app.ErrTemplateConflict)

		_, err = load(t, "myapp", app.Options{Template: "default", Start: start})
		requireFailure(t, err, app.ErrTemplateConflict)
	})

	t.Run("the host pair of scripts and processes needs no config", func(t *testing.T) {
		hostDir := filepath.Join(system, "etc", "myapp")
		writeTree(t, hostDir, map[string]string{
			"instances.yaml": "legacy-1:\n  script: init.lua\nworker-1:\n  binary: python3\n",
		})
		t.Cleanup(func() { _ = os.RemoveAll(hostDir) })

		got, err := load(t, "myapp", app.Options{Start: start})
		require.NoError(t, err)
		assert.Equal(t, app.Pair{
			Template:      "",
			Dir:           hostDir,
			InstancesFile: filepath.Join(hostDir, "instances.yaml"),
			ConfigFile:    "",
		}, got.Pair)
		assert.Nil(t, got.Config)
		assert.Equal(t, []string{"legacy-1", "worker-1"}, names(got.Instances))
		assert.Equal(t, filepath.Join(versionDir, "init.lua"), got.Instances[0].Script,
			"the script is in the version")
	})

	for _, file := range []string{"instances.yaml", "config.yaml"} {
		t.Run("an incomplete host pair with only "+file, func(t *testing.T) {
			hostDir := filepath.Join(system, "etc", "myapp")
			writeTree(t, hostDir, map[string]string{file: "storage-1:\n"})
			t.Cleanup(func() { _ = os.RemoveAll(hostDir) })

			_, err := load(t, "myapp", app.Options{Start: start})
			requireFailure(t, err, app.ErrIncompletePair)
			require.ErrorContains(t, err, "the pair of the host in "+hostDir)
		})
	}

	t.Run("a missing template", func(t *testing.T) {
		_, err := load(t, "myapp", app.Options{Template: "prod", Start: start})
		requireFailure(t, err, app.ErrNoPair)
		require.ErrorContains(t, err, "its templates are default, staging")
	})

	t.Run("an invalid template name", func(t *testing.T) {
		_, err := load(t, "myapp", app.Options{Template: "../x", Start: start})
		requireFailure(t, err, app.ErrInvalidTemplate)
	})
}

func TestProjectPair(t *testing.T) {
	roots(t)

	t.Run("no templates", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{"app.manifest.toml": manifestOf("myapp")})

		_, err := load(t, "", app.Options{Start: tree})
		requireFailure(t, err, app.ErrNoPair)
		require.ErrorContains(t, err, "has no instances.yaml in "+
			filepath.Join(tree, ".templates", "default"))
	})

	t.Run("no default template", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, templateFiles(".", "prod", "storage-1"))

		_, err := load(t, "", app.Options{Start: tree})
		requireFailure(t, err, app.ErrNoPair)
		require.ErrorContains(t, err, "its templates are prod, pick one with --template")

		got, err := load(t, "", app.Options{Template: "prod", Start: tree})
		require.NoError(t, err)
		assert.Equal(t, "prod", got.Pair.Template)
	})

	t.Run("a template without config", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": "legacy-1:\n  script: init.lua\n" +
				"storage-1:\nstorage-2:\n",
		})

		_, err := load(t, "", app.Options{Start: tree})
		requireFailure(t, err, app.ErrIncompletePair)
		require.ErrorContains(t, err, "has instances.yaml but no config.yaml next to it "+
			`to configure its Tarantool instances "storage-1", "storage-2"`)
	})

	t.Run("a template without config of scripts and processes", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": "legacy-1:\n  script: init.lua\n" +
				"worker-1:\n  binary: ./bin/worker\n",
		})

		got, err := load(t, "", app.Options{Start: tree})
		require.NoError(t, err)
		assert.Equal(t, []string{"legacy-1", "worker-1"}, names(got.Instances))
		assert.Empty(t, got.Pair.ConfigFile, "the pair has no config")
		assert.Nil(t, got.Config)
		assert.False(t, got.HasClusterInstances())
		assert.Equal(t, filepath.Join(tree, ".templates", "default", "instances.yaml"),
			got.Pair.InstancesFile)
	})

	t.Run("an empty template without config", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{".templates/default/instances.yaml": ""})

		got, err := load(t, "", app.Options{Start: tree})
		require.NoError(t, err)
		assert.Empty(t, got.Instances)

		_, err = app.Resolve(context.Background(), nil, app.Options{Start: tree})
		requireFailure(t, err, app.ErrNoInstances)
	})

	t.Run("the config of scripts and processes is not read", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": "legacy-1:\n  script: init.lua\n",
			// cluster.GetClusterConfig refuses an empty config, and one that
			// sends it to a storage out of reach.
			".templates/default/config.yaml": "config:\n  etcd:\n    endpoints: " +
				"[http://127.0.0.1:1]\n    prefix: /myapp\n",
		})

		repo := &fileRepository{}

		got, err := load(t, "", app.Options{
			Start:     tree,
			Integrity: integrity.IntegrityCtx{Repository: repo},
		})
		require.NoError(t, err)
		assert.Nil(t, got.Config)
		assert.Equal(t, filepath.Join(tree, ".templates", "default", "config.yaml"),
			got.Pair.ConfigFile, "the pair has a config, which is left unread")
		assert.Equal(t, []string{filepath.Join(tree, ".templates", "default",
			"instances.yaml")}, repo.read)

		writeTree(t, tree, map[string]string{".templates/default/config.yaml": ""})

		_, err = load(t, "", app.Options{Start: tree})
		require.NoError(t, err)
	})

	t.Run("a template without instances", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			"app.manifest.toml":              manifestOf("myapp"),
			".templates/default/config.yaml": topology("storage-1"),
		})

		_, err := load(t, "", app.Options{Start: tree})
		requireFailure(t, err, app.ErrIncompletePair)
		require.ErrorContains(t, err, "has config.yaml but no instances.yaml")
	})

	t.Run("a project ignores the host pair", func(t *testing.T) {
		_, system := roots(t)
		writeTree(t, filepath.Join(system, "etc", "myapp"), map[string]string{
			"instances.yaml": "host-1:\n",
			"config.yaml":    topology("host-1"),
		})

		tree := t.TempDir()
		writeTree(t, tree, merge(templateFiles(".", "default", "storage-1"),
			map[string]string{"app.manifest.toml": manifestOf("myapp")}))

		got, err := load(t, "myapp", app.Options{Start: tree})
		require.NoError(t, err)
		assert.Equal(t, app.ScopeProject, got.Scope)
		assert.Equal(t, []string{"storage-1"}, names(got.Instances))
	})
}

func TestPairContents(t *testing.T) {
	cases := []struct {
		name   string
		desc   string
		config string
		want   []string // The names of the records.
		err    error
		msg    []string // Parts of the message.
	}{
		{
			name:   "the topology may hold more",
			desc:   "storage-1:\n",
			config: topology("storage-1", "storage-2", "router-1"),
			want:   []string{"storage-1"},
		},
		{
			name: "script and binary records are not in the topology",
			desc: "storage-1:\nlegacy-1:\n  script: init.lua\n" +
				"worker-1:\n  binary: ./bin/worker\n  args: [--port, \"8080\"]\n" +
				"  workdir: var/worker\n  logrotate_signal: HUP\n",
			config: topology("storage-1"),
			want:   []string{"storage-1", "legacy-1", "worker-1"},
		},
		{
			name:   "a Tarantool instance missing from the topology",
			desc:   "storage-1:\nstorage-9:\nrouter-9:\n",
			config: topology("storage-1"),
			err:    app.ErrTopologyMismatch,
			msg:    []string{`"storage-9", "router-9"`, "not declared in"},
		},
		{
			name:   "a config without a topology",
			desc:   "storage-1:\n",
			config: "app:\n  file: init.lua\n",
			err:    app.ErrTopologyMismatch,
		},
		{
			name:   "workdir of a cluster instance",
			desc:   "storage-1:\n  workdir: var\n",
			config: topology("storage-1"),
			err:    app.ErrUnsupportedField,
			msg:    []string{`instance "storage-1"`, "workdir"},
		},
		{
			name:   "args of a script instance",
			desc:   "legacy-1:\n  script: init.lua\n  args: [-x]\n",
			config: topology(),
			err:    app.ErrUnsupportedField,
			msg:    []string{`instance "legacy-1"`, "args"},
		},
		{
			name:   "logrotate_signal of a cluster instance",
			desc:   "storage-1:\n  logrotate_signal: SIGHUP\n",
			config: topology("storage-1"),
			err:    app.ErrUnsupportedField,
			msg:    []string{`instance "storage-1"`, "logrotate_signal", "SIGHUP"},
		},
		{
			name:   "logrotate_signal of a script instance",
			desc:   "legacy-1:\n  script: init.lua\n  logrotate_signal: USR1\n",
			config: topology(),
			err:    app.ErrUnsupportedField,
			msg:    []string{`instance "legacy-1"`, "logrotate_signal"},
		},
		{
			name:   "an invalid description",
			desc:   "storage-1:\n  colour: red\n",
			config: topology("storage-1"),
			err:    instances.ErrInvalid,
			msg:    []string{"colour"},
		},
		{
			name:   "an invalid config",
			desc:   "storage-1:\n",
			config: "groups: [\n",
			msg:    []string{"config.yaml"},
		},
		{
			name:   "an empty description",
			desc:   "",
			config: topology("storage-1"),
			want:   []string{},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			roots(t)

			tree := t.TempDir()
			writeTree(t, tree, map[string]string{
				".templates/default/instances.yaml": test.desc,
				".templates/default/config.yaml":    test.config,
			})

			got, err := load(t, "", app.Options{Start: tree})
			if test.want != nil {
				require.NoError(t, err)
				assert.Equal(t, test.want, names(got.Instances))

				return
			}

			require.Error(t, err)
			assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))

			if test.err != nil {
				require.ErrorIs(t, err, test.err)
			}

			for _, msg := range test.msg {
				require.ErrorContains(t, err, msg)
			}
		})
	}
}

// closedAddress returns an address on the loopback nobody listens on.
func closedAddress(t *testing.T) string {
	t.Helper()

	var config net.ListenConfig

	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

// unreachableConfig returns a thin cluster config that sends tt for the rest
// of the config to an etcd nobody listens on.
func unreachableConfig(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("config:\n  etcd:\n    endpoints:\n      - http://%s\n"+
		"    prefix: /myapp\n    http:\n      request:\n        timeout: 0.5\n",
		closedAddress(t))
}

// TestLoadBestEffortConfig pins that Options.BestEffortConfig reads the
// cluster config of the pair as far as it can be read: a config that does
// not parse, one that sends tt to an etcd out of reach and a pair without a
// config load all the same, without Config and with ConfigError; a config
// that can be read is read, whether its topology declares the instances or
// not. The description is read and checked as always.
func TestLoadBestEffortConfig(t *testing.T) {
	loaded := []struct {
		name   string
		desc   string
		config string // The file is left out when it is empty.
		read   bool   // Whether the config is read.
	}{
		{name: "a config that does not parse", desc: "storage-1:\n", config: "groups: [\n"},
		{name: "a config storage out of reach", desc: "storage-1:\n", config: "unreachable"},
		{
			name: "a topology without the instance", desc: "storage-9:\n",
			config: topology("storage-1"), read: true,
		},
		{name: "no config", desc: "storage-1:\n"},
	}

	for _, test := range loaded {
		t.Run(test.name, func(t *testing.T) {
			roots(t)

			config := test.config
			if config == "unreachable" {
				config = unreachableConfig(t)
			}

			files := map[string]string{".templates/default/instances.yaml": test.desc}
			if config != "" {
				files[".templates/default/config.yaml"] = config
			}

			tree := t.TempDir()
			writeTree(t, tree, files)

			_, err := load(t, "", app.Options{Start: tree})
			require.Error(t, err, "the config is required without BestEffortConfig")

			got, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
			require.NoError(t, err)
			assert.True(t, got.BestEffortConfig)
			assert.Equal(t, []string{strings.TrimSuffix(test.desc, ":\n")}, names(got.Instances))
			assert.True(t, got.HasClusterInstances())

			if test.read {
				assert.NotNil(t, got.Config)
				require.NoError(t, got.ConfigError)

				return
			}

			assert.Nil(t, got.Config)
			require.Error(t, got.ConfigError)
			assert.Contains(t, got.ConfigError.Error(), `application "`)
		})
	}

	t.Run("a config that can be read", func(t *testing.T) {
		roots(t)

		tree := t.TempDir()
		writeTree(t, tree, templateFiles(".", "default", "storage-1"))

		got, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
		require.NoError(t, err)
		assert.NotNil(t, got.Config)
		require.NoError(t, got.ConfigError)

		got, err = load(t, "", app.Options{Start: tree})
		require.NoError(t, err)
		assert.False(t, got.BestEffortConfig)
	})

	failed := []struct {
		name  string
		files map[string]string
		err   error
	}{
		{
			name: "an invalid description",
			files: map[string]string{
				".templates/default/instances.yaml": "storage-1:\n  colour: red\n",
				".templates/default/config.yaml":    "groups: [\n",
			},
			err: instances.ErrInvalid,
		},
		{
			name: "a field a Tarantool instance cannot honour",
			files: map[string]string{
				".templates/default/instances.yaml": "storage-1:\n  workdir: var\n",
				".templates/default/config.yaml":    "groups: [\n",
			},
			err: app.ErrUnsupportedField,
		},
		{
			name: "a config without a description",
			files: map[string]string{
				".templates/default/config.yaml": topology("storage-1"),
				"app.manifest.toml":              manifestOf("myapp"),
			},
			err: app.ErrIncompletePair,
		},
	}

	for _, test := range failed {
		t.Run(test.name, func(t *testing.T) {
			roots(t)

			tree := t.TempDir()
			writeTree(t, tree, test.files)

			_, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
			requireFailure(t, err, test.err)
		})
	}
}

// fileRepository reads files from the filesystem and records the paths it
// was asked for.
type fileRepository struct {
	read []string
}

func (repo *fileRepository) Read(path string) (io.ReadCloser, error) {
	repo.read = append(repo.read, path)

	return os.Open(path)
}

func (repo *fileRepository) ReadFile(path string) ([]byte, error) {
	repo.read = append(repo.read, path)

	return os.ReadFile(path)
}

func (repo *fileRepository) ValidateAll() error {
	return nil
}

func TestLoadReadsThroughIntegrity(t *testing.T) {
	roots(t)

	tree := t.TempDir()
	writeTree(t, tree, merge(templateFiles(".", "default", "storage-1"),
		map[string]string{"app.manifest.toml": manifestOf("myapp")}))

	repo := &fileRepository{}

	_, err := load(t, "", app.Options{
		Start:     tree,
		Integrity: integrity.IntegrityCtx{Repository: repo},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(tree, "app.manifest.toml"),
		filepath.Join(tree, ".templates", "default", "instances.yaml"),
	}, repo.read)
}
