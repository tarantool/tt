package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/pack"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
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

// TestPackInstallRoundtripCarriesTemplates drives the instance templates
// through the real pack writer and the real install reader: the project's
// .templates/ reaches the deployed install-root intact, where tt instance looks
// for them, even though no include rule names it and the component filter
// drops every hidden name.
func TestPackInstallRoundtripCarriesTemplates(t *testing.T) {
	t.Parallel()

	// Compared byte for byte below: pack and install copy the files, they do
	// not re-render them.
	instancesBody := "router-1:\nworker-1:\n  binary: ./bin/worker\n"
	configBody := "groups:\n  routers: {}\n"

	src := t.TempDir()
	writeSource(t, src, map[string]string{
		"app.manifest.toml":                 roundtripManifest,
		"VERSION":                           "1.0.0\n",
		"init.lua":                          "return 1\n",
		"README.md":                         "round-app\n",
		".templates/default/instances.yaml": instancesBody,
		".templates/default/config.yaml":    configBody,
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

	// The build never routes the templates through the component tree.
	assert.NoDirExists(t,
		filepath.Join(src, ".rocks", "share", "tarantool", "round-app", ".templates"))

	deploy := t.TempDir()

	_, err = Run(context.Background(), Options{
		ProjectDir: deploy,
		Scope:      ScopeProject,
		Archives:   []string{packed.Path},
		Yes:        true,
	})
	require.NoError(t, err)

	assert.Equal(t, instancesBody,
		readFile(t, filepath.Join(deploy, ".templates", "default", "instances.yaml")))
	assert.Equal(t, configBody,
		readFile(t, filepath.Join(deploy, ".templates", "default", "config.yaml")))
	assert.FileExists(t,
		filepath.Join(deploy, ".rocks", "share", "tarantool", "round-app", "init.lua"))
}

// TestPackInstallRoundtripUserScope drives the real pack writer into an
// install into the user scope: the archive becomes one version directory
// laid out like a project — manifest, lock and VERSION at its top, the
// package in .rocks/, the templates in .templates/ — and apps/<app> selects
// it. Not parallel: the user root comes from TT_USER_ROOT.
func TestPackInstallRoundtripUserScope(t *testing.T) {
	root := userRoot(t)

	src := t.TempDir()
	writeSource(t, src, map[string]string{
		"app.manifest.toml":                 roundtripManifest,
		"VERSION":                           "1.0.0\n",
		"init.lua":                          "return 1\n",
		"README.md":                         "round-app\n",
		".templates/default/instances.yaml": "router-1:\n",
		".templates/default/config.yaml":    "groups: {}\n",
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

	_, err = Run(context.Background(), Options{
		ProjectDir: t.TempDir(),
		Scope:      ScopeUser,
		Archives:   []string{packed.Path},
		Yes:        true,
	})
	require.NoError(t, err)

	dir := filepath.Join(root, "versions", "round-app", "round-app_1.0.0")
	assert.Equal(t, roundtripManifest, readFile(t, filepath.Join(dir, "app.manifest.toml")))
	assert.FileExists(t, filepath.Join(dir, "app.manifest.lock"))
	assert.Equal(t, "1.0.0\n", readFile(t, filepath.Join(dir, "VERSION")))
	assert.Equal(t, "round-app\n", readFile(t, filepath.Join(dir, "README.md")))
	assert.FileExists(t,
		filepath.Join(dir, ".rocks", "share", "tarantool", "round-app", "init.lua"))
	assert.FileExists(t, filepath.Join(dir, ".templates", "default", "instances.yaml"))

	target, err := os.Readlink(filepath.Join(root, "apps", "round-app"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("..", "versions", "round-app", "round-app_1.0.0"), target)
}

// TestPackRefusesBrokenTemplate: the real pack fails on an incomplete template
// and writes no archive, so a broken description never reaches a host.
func TestPackRefusesBrokenTemplate(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeSource(t, src, map[string]string{
		"app.manifest.toml":                 roundtripManifest,
		"VERSION":                           "1.0.0\n",
		"init.lua":                          "return 1\n",
		"README.md":                         "round-app\n",
		".templates/default/instances.yaml": "router-1:\n",
	})

	out := filepath.Join(src, "_out")

	_, err := pack.Run(context.Background(), pack.Options{
		ProjectDir:  src,
		WithoutDeps: true,
		OutputDir:   out,
		Build: build.Options{
			TtVersion: "tt test",
			Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".templates/default")
	assert.Contains(t, err.Error(), "config.yaml")
	assert.NoDirExists(t, out)
}

// writeSource writes a set of files into dir, creating parents.
func writeSource(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}
