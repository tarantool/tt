package app_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
)

// resolveFixture lays down a project called myapp with router-1, storage-1
// and storage-2, in that order, and an application other in the system scope
// with worker-1. It returns the project directory.
func resolveFixture(t *testing.T) string {
	t.Helper()

	_, system := roots(t)

	tree := t.TempDir()
	writeTree(t, tree, merge(templateFiles(".", "default", "router-1", "storage-1", "storage-2"),
		map[string]string{
			"app.manifest.toml": manifestOf("myapp"),
			"src/.keep":         "",
		}))
	install(t, system, "other", "1.0.0", templateFiles(".", "default", "worker-1"))

	return tree
}

// resolve parses args and resolves them from start.
func resolve(t *testing.T, start string, args ...string) ([]app.Selection, error) {
	t.Helper()

	targets, err := app.ParseTargets(args)
	require.NoError(t, err)

	return app.Resolve(t.Context(), targets, app.Options{Start: start})
}

func TestResolve(t *testing.T) {
	projectDir := resolveFixture(t)
	start := filepath.Join(projectDir, "src")

	type selected struct {
		app       string
		instances []string
	}

	cases := []struct {
		name string
		args []string
		want []selected
	}{
		{
			name: "no target selects the project",
			args: nil,
			want: []selected{{"myapp", []string{"router-1", "storage-1", "storage-2"}}},
		},
		{
			name: "every instance",
			args: []string{"myapp:*"},
			want: []selected{{"myapp", []string{"router-1", "storage-1", "storage-2"}}},
		},
		{
			name: "one instance",
			args: []string{"myapp:storage-1"},
			want: []selected{{"myapp", []string{"storage-1"}}},
		},
		{
			name: "a list follows the description, once each",
			args: []string{"myapp:storage-2,myapp:router-1", "myapp:storage-2"},
			want: []selected{{"myapp", []string{"router-1", "storage-2"}}},
		},
		{
			name: "every instance wins over a list",
			args: []string{"myapp:storage-2,myapp:*"},
			want: []selected{{"myapp", []string{"router-1", "storage-1", "storage-2"}}},
		},
		{
			name: "applications in the order first named",
			args: []string{"other:worker-1,myapp:storage-1", "other:*"},
			want: []selected{
				{"other", []string{"worker-1"}},
				{"myapp", []string{"storage-1"}},
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolve(t, start, test.args...)
			require.NoError(t, err)

			list := make([]selected, 0, len(got))
			for _, selection := range got {
				list = append(list, selected{selection.App.Name, names(selection.Instances)})
			}

			assert.Equal(t, test.want, list)
		})
	}
}

func TestResolveScopes(t *testing.T) {
	projectDir := resolveFixture(t)

	got, err := resolve(t, projectDir, "myapp:*", "other:*")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, app.ScopeProject, got[0].App.Scope)
	assert.Equal(t, app.ScopeSystem, got[1].App.Scope)
}

func TestResolveErrors(t *testing.T) {
	projectDir := resolveFixture(t)

	t.Run("an instance the description lacks", func(t *testing.T) {
		_, err := resolve(t, projectDir, "myapp:storage-1,myapp:storage-9,myapp:router-9")
		requireFailure(t, err, app.ErrInstanceNotFound)
		require.ErrorContains(t, err, `"storage-9", "router-9" of "myapp" not in`)
		require.ErrorContains(t, err, "it describes router-1, storage-1, storage-2")
	})

	t.Run("an instance only the topology knows", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": "storage-1:\n",
			".templates/default/config.yaml":    topology("storage-1", "storage-2"),
		})

		_, err := resolve(t, tree, filepath.Base(tree)+":storage-2")
		requireFailure(t, err, app.ErrInstanceNotFound)
	})

	t.Run("no target outside of any project", func(t *testing.T) {
		_, err := resolve(t, t.TempDir())
		requireFailure(t, err, app.ErrNoProject)
		require.ErrorContains(t, err, "use <app>:<instance> or <app>:*")
	})

	t.Run("a description without instances", func(t *testing.T) {
		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": "# nothing yet\n",
			".templates/default/config.yaml":    topology("storage-1"),
		})

		_, err := resolve(t, tree)
		requireFailure(t, err, app.ErrNoInstances)

		_, err = resolve(t, tree, filepath.Base(tree)+":*")
		requireFailure(t, err, app.ErrNoInstances)
	})

	t.Run("the first failing application fails the call", func(t *testing.T) {
		got, err := resolve(t, projectDir, "myapp:*", "missing:*")
		requireFailure(t, err, app.ErrAppNotFound)
		assert.Nil(t, got)
	})
}
