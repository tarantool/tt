package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/scope"
)

// TestNames pins the applications completion offers: the project the
// command runs in, then the applications of the system scope, or of the
// scope --scope names; the user scope only with --scope user.
func TestNames(t *testing.T) {
	user, system := roots(t)

	tree := t.TempDir()
	writeTree(t, tree, merge(
		templateFiles("work", "default", "local-1"),
		map[string]string{
			"work/app.manifest.toml":                   manifestOf("myapp"),
			"work/src/.keep":                           "",
			"broken/app.manifest.toml":                 "not a manifest",
			"broken/.templates/default/instances.yaml": "",
		}))
	install(t, system, "myapp", "1.0.0", templateFiles(".", "default", "system-1"))
	install(t, system, "billing", "1.0.0", templateFiles(".", "default", "b-1"))
	install(t, user, "mine", "1.0.0", templateFiles(".", "default", "m-1"))

	// Neither a file nor a dangling link under apps/ is an application.
	require.NoError(t, os.WriteFile(filepath.Join(system, "apps", "stray"), nil, 0o644))
	require.NoError(t, os.Symlink("gone", filepath.Join(system, "apps", "dangling")))

	cases := []struct {
		name  string
		start string
		scope app.Scope
		want  []string
	}{
		{
			name: "inside a project", start: "work/src",
			want: []string{"myapp", "billing"},
		},
		{
			name: "outside of any project", start: ".",
			want: []string{"billing", "myapp"},
		},
		{
			name: "a project that does not name itself", start: "broken",
			want: []string{"billing", "myapp"},
		},
		{
			name: "--scope project", start: "work", scope: app.ScopeProject,
			want: []string{"myapp"},
		},
		{
			name: "--scope project outside of any project", start: ".",
			scope: app.ScopeProject, want: nil,
		},
		{
			name: "--scope user", start: "work", scope: app.ScopeUser,
			want: []string{"mine"},
		},
		{
			name: "--scope system", start: "work", scope: app.ScopeSystem,
			want: []string{"billing", "myapp"},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := app.Names(app.Options{
				Scope: test.scope,
				Start: filepath.Join(tree, test.start),
			})
			assert.Equal(t, test.want, got)
		})
	}
}

// TestNamesWithoutScopeRoot pins that a scope root that is not there offers
// no application rather than an error.
func TestNamesWithoutScopeRoot(t *testing.T) {
	roots(t)

	t.Setenv(scope.SystemRootEnv, filepath.Join(t.TempDir(), "missing"))

	assert.Empty(t, app.Names(app.Options{Start: t.TempDir()}))
}
