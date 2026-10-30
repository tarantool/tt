package scope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/scope"
)

// versionOf returns the directory of a version of app under root, created.
func versionOf(t *testing.T, root, app string) string {
	t.Helper()

	dir := filepath.Join(root, "versions", app, app+"_1.0.0")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	return dir
}

func TestVarLinks(t *testing.T) {
	links := scope.VarLinks("/opt/tarantool", "app", "/opt/tarantool/versions/app/app_1.0.0")

	assert.Equal(t, []scope.VarLink{
		{
			Path:   "/opt/tarantool/versions/app/app_1.0.0/var/lib",
			Target: "/opt/tarantool/var/lib/app",
		},
		{
			Path:   "/opt/tarantool/versions/app/app_1.0.0/var/run",
			Target: "/opt/tarantool/var/run/app",
		},
		{
			Path:   "/opt/tarantool/versions/app/app_1.0.0/var/log",
			Target: "/opt/tarantool/var/log/app",
		},
	}, links)
}

// LinkVar makes relative links, which lead to the layout before it exists
// and once it does, and checks them when they are there.
func TestLinkVar(t *testing.T) {
	root := t.TempDir()
	version := versionOf(t, root, "app")

	missing, err := scope.CheckVarLinks(root, "app", version)
	require.NoError(t, err)
	assert.True(t, missing)

	require.NoError(t, scope.LinkVar(root, "app", version, 0o755))

	for _, kind := range []string{"lib", "run", "log"} {
		target, err := os.Readlink(filepath.Join(version, "var", kind))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("..", "..", "..", "..", "var", kind, "app"), target)
	}

	missing, err = scope.CheckVarLinks(root, "app", version)
	require.NoError(t, err)
	assert.False(t, missing)

	// Once the layout exists, what is written through a link is in it.
	data := filepath.Join(root, "var", "lib", "app", "storage-1")
	require.NoError(t, os.MkdirAll(data, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(version, "var", "lib", "storage-1", "x"),
		[]byte("x"), 0o644))
	assert.FileExists(t, filepath.Join(data, "x"))

	// A second run finds the links in place and changes nothing.
	require.NoError(t, scope.LinkVar(root, "app", version, 0o755))

	// One missing link is made again.
	require.NoError(t, os.Remove(filepath.Join(version, "var", "run")))

	missing, err = scope.CheckVarLinks(root, "app", version)
	require.NoError(t, err)
	assert.True(t, missing)

	require.NoError(t, scope.LinkVar(root, "app", version, 0o755))

	missing, err = scope.CheckVarLinks(root, "app", version)
	require.NoError(t, err)
	assert.False(t, missing)
}

// A root reached through a link of its own still gets links that lead into
// it.
func TestLinkVarLinkedRoot(t *testing.T) {
	target := t.TempDir()
	root := filepath.Join(t.TempDir(), "root")
	require.NoError(t, os.Symlink(target, root))

	version := versionOf(t, root, "app")

	require.NoError(t, scope.LinkVar(root, "app", version, 0o755))

	missing, err := scope.CheckVarLinks(root, "app", version)
	require.NoError(t, err)
	assert.False(t, missing)
}

// What is not the link to the layout is refused, and left as it is.
func TestCheckVarLinksRefuses(t *testing.T) {
	cases := map[string]func(t *testing.T, version string){
		"a directory": func(t *testing.T, version string) {
			t.Helper()
			require.NoError(t, os.MkdirAll(filepath.Join(version, "var", "lib", "storage-1"),
				0o755))
		},
		"a file": func(t *testing.T, version string) {
			t.Helper()
			require.NoError(t, os.MkdirAll(filepath.Join(version, "var"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(version, "var", "run"), nil, 0o644))
		},
		"a link elsewhere": func(t *testing.T, version string) {
			t.Helper()
			require.NoError(t, os.MkdirAll(filepath.Join(version, "var"), 0o755))
			require.NoError(t, os.Symlink("/tmp", filepath.Join(version, "var", "log")))
		},
	}

	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			version := versionOf(t, root, "app")
			prepare(t, version)

			_, err := scope.CheckVarLinks(root, "app", version)
			require.ErrorIs(t, err, scope.ErrVarLink)

			err = scope.LinkVar(root, "app", version, 0o755)
			require.ErrorIs(t, err, scope.ErrVarLink)
		})
	}
}
