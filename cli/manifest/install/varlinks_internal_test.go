package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// requireVarLinks checks that var/ of the installed version dir holds the
// links to the layout of the application app under root, and nothing else.
func requireVarLinks(t *testing.T, root, app, dir string) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(dir, "var"))
	require.NoError(t, err)
	require.Len(t, entries, 3)

	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	realVar, err := filepath.EvalSymlinks(filepath.Join(dir, "var"))
	require.NoError(t, err)

	for _, kind := range []string{"lib", "log", "run"} {
		target, err := os.Readlink(filepath.Join(dir, "var", kind))
		require.NoError(t, err, "var/%s", kind)
		assert.False(t, filepath.IsAbs(target), "var/%s leads to %s", kind, target)
		assert.Equal(t, filepath.Join(realRoot, "var", kind, app),
			filepath.Join(realVar, target), "var/%s", kind)
	}
}

// A signed install links var/lib, var/run and var/log of the version to the
// layout of its instances, which it does not create: tt creates the
// directories of an instance when it starts it, as the user it runs as.
func TestInstallSignedLinksLayout(t *testing.T) {
	root := userRoot(t)
	signer, checks := signingKeys(t)

	require.NoError(t, installSigned(t, packRoundtrip(t, signer), checks))

	version := filepath.Join(root, "versions", "round-app", "round-app_1.0.0")
	requireVarLinks(t, root, "round-app", version)
	assert.NoDirExists(t, filepath.Join(root, "var", "lib"))

	// The links are not files of the signed tree.
	require.NoError(t, checks.VerifyTree(version))
}

// An install that is not signed lays the version out as before: no var/.
func TestInstallUnsignedNoVarLinks(t *testing.T) {
	root := userRoot(t)

	_, err := Run(context.Background(), Options{
		ProjectDir: t.TempDir(),
		Scope:      ScopeUser,
		Archives:   []string{packRoundtrip(t, nil)},
		Yes:        true,
	})
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(root, "versions", "round-app", "round-app_1.0.0", "var"))
	assert.NoDirExists(t, filepath.Join(root, "versions", "round-app", "round-app_1.0.0", "var"))
}

// A version replaced by --force and an application uninstalled lose their
// links, not what the links lead to: the data of the instances outlives
// every version.
func TestInstallSignedKeepsDataOfInstances(t *testing.T) {
	root := userRoot(t)
	signer, checks := signingKeys(t)
	archive := packRoundtrip(t, signer)

	require.NoError(t, installSigned(t, archive, checks))

	data := filepath.Join(root, "var", "lib", "round-app", "storage-1", "00000.snap")
	require.NoError(t, os.MkdirAll(filepath.Dir(data), 0o755))
	require.NoError(t, os.WriteFile(data, []byte("data\n"), 0o644))

	_, err := Run(context.Background(), Options{
		ProjectDir: t.TempDir(),
		Scope:      ScopeUser,
		Archives:   []string{archive},
		Yes:        true,
		Force:      true,
		Integrity:  checks,
	})
	require.NoError(t, err)

	version := filepath.Join(root, "versions", "round-app", "round-app_1.0.0")
	requireVarLinks(t, root, "round-app", version)
	assert.FileExists(t, data)

	// The data is reached through the links of the version.
	got, err := os.ReadFile(filepath.Join(version, "var", "lib", "storage-1", "00000.snap"))
	require.NoError(t, err)
	assert.Equal(t, "data\n", string(got))

	require.NoError(t, state.RemoveApp(root, "round-app"))
	assert.NoDirExists(t, version)
	assert.FileExists(t, data)
}
