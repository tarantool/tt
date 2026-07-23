package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/build"
	"github.com/tarantool/tt/cli/manifest/pack"
	"github.com/tarantool/tt/cli/manifest/rocks"
)

// roundtripManifest is a dependency-free pure-Lua package: it packs and installs
// with no compiler, no registry and no tarantool binary.
const roundtripManifest = `manifest_version = '0.1'

[package]
name = 'round-app'
include = ['README.md']

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
include = ['*.lua']
`

// TestPackInstallRoundtrip drives the real pack writer into the real install
// reader: a --without-deps archive of a dependency-free app is packed, then
// installed offline into a fresh project, and its files and metadata land where
// they belong. This is the headline offline path with no fabricated archive in
// the middle.
func TestPackInstallRoundtrip(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeSource(t, src, map[string]string{
		"app.manifest.toml": roundtripManifest,
		"VERSION":           "1.0.0\n",
		"init.lua":          "return 1\n",
		"README.md":         "round-app\n",
	})

	packed, err := pack.Run(context.Background(), pack.Options{
		ProjectDir:  src,
		WithoutDeps: true,
		OutputDir:   filepath.Join(src, "_out"),
		Build: build.Options{
			TtVersion: "tt test",
			Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
		},
	})
	require.NoError(t, err)

	deploy := t.TempDir()
	result, err := Run(context.Background(), Options{
		ProjectDir: deploy,
		Scope:      ScopeProject,
		Archives:   []string{packed.Path},
		Yes:        true,
	})
	require.NoError(t, err)
	require.Len(t, result.Installed, 1)
	assert.Equal(t, "round-app", result.Installed[0].Package)

	// The package's own files land in the shared tree.
	assert.FileExists(t,
		filepath.Join(deploy, ".rocks", "share", "tarantool", "round-app", "init.lua"))
	assert.FileExists(t,
		filepath.Join(deploy, ".rocks", "share", "tarantool", "round-app", "version.lua"))

	// Metadata lands under manifests/ for list/uninstall.
	metaDir := filepath.Join(deploy, ".rocks", "manifests", "round-app")
	assert.FileExists(t, filepath.Join(metaDir, "manifest.toml"))
	assert.FileExists(t, filepath.Join(metaDir, "lock.toml"))
	assert.Equal(t, "1.0.0\n", readFile(t, filepath.Join(metaDir, "VERSION")))

	// The project's own manifest is not overwritten by the guest's.
	assert.NoFileExists(t, filepath.Join(deploy, "app.manifest.toml"))
}

// writeSource writes a set of files into dir, creating parents.
func writeSource(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644)) //nolint:gosec
	}
}
