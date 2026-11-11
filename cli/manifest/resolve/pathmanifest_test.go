package resolve_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
)

// writeTree writes files, by slash path relative to root, under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// contribManifest is a neighbour project: a global registry dependency, a
// component one, one from a registry of its own, a path dependency of its own
// and a dev dependency no consumer resolves.
const contribManifest = `manifest_version = '0.1'
[package]
name = 'contrib'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
metrics = '>=1.0.0'
helper = { source = 'path', path = '../helper' }
scoped = { version = '>=1.0.0', registry = 'local-rocks' }
[dev_dependencies]
luatest = '>=1.0.0'
[components.lib]
path = '.'
[components.lib.dependencies]
checks = '>=3.0.0'
[products.default]
components = ['lib']
default = true
`

// consumerManifest declares contrib by path.
const consumerManifest = oneProduct + `[dependencies]
contrib = { source = 'path', path = '../libs/contrib' }
`

// pathManifestLayout lays out a consumer at <base>/app and the neighbour it
// depends on at <base>/libs/contrib, with the neighbour's own path dependency
// at <base>/libs/helper, and returns the directories of the consumer and the
// neighbour.
func pathManifestLayout(t *testing.T, contrib string) (string, string) {
	t.Helper()

	base := t.TempDir()
	app := filepath.Join(base, "app")
	neighbour := filepath.Join(base, "libs", "contrib")

	writeTree(t, app, map[string]string{"init.lua": "return {}\n"})
	writeTree(t, neighbour, map[string]string{
		"app.manifest.toml": contrib,
		"VERSION":           "1.4.0-rc.1\n",
		"contrib/init.lua":  "return {}\n",
	})
	writeTree(t, filepath.Join(base, "libs", "helper"), map[string]string{
		"helper.lua": "return 1\n",
	})

	return app, neighbour
}

// contribAdapter serves the registry rocks contribManifest depends on, the
// scoped one from the neighbour's local registry only.
func contribAdapter(t *testing.T, neighbour string) *fakeAdapter {
	t.Helper()

	return newFakeAdapter().
		add("metrics", "1.5.0-1", "m").
		add("checks", "3.3.0-1", "c").
		addScoped(filepath.Join(neighbour, "local-rocks"), "scoped", "1.0.0-1", "s",
			dep(t, "checks", ">=3.0.0"))
}

func TestPathManifestResolvesFromTheManifest(t *testing.T) {
	t.Parallel()

	app, neighbour := pathManifestLayout(t, contribManifest)
	man := parseManifest(t, consumerManifest)

	engine := resolve.NewEngine(contribAdapter(t, neighbour), app, "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	closure := lock.Products["default"].Dependencies

	// The neighbour's [dev_dependencies] are its own: luatest is not resolved
	// (the fake registry does not even serve it).
	assert.ElementsMatch(t,
		[]string{"contrib", "metrics", "checks", "helper", "scoped"}, depNames(closure))
	assert.Equal(t, "contrib", closure[len(closure)-1].Name,
		"the neighbour comes after its own dependencies")

	contrib := findDep(t, closure, "contrib")
	assert.Equal(t, "1.4.0.rc.1-1", contrib.Version, "the version of the rock")
	assert.Equal(t, "path", contrib.Source)
	assert.Equal(t, "../libs/contrib", contrib.Path)
	assert.True(t, strings.HasPrefix(contrib.ContentHash, "sha256:"))

	// The neighbour's path dependency is written relative to the consumer.
	helper := findDep(t, closure, "helper")
	assert.Equal(t, "path", helper.Source)
	assert.Equal(t, "../libs/helper", helper.Path)

	// The neighbour's registry is a directory relative to the neighbour.
	assert.Equal(t, "1.0.0-1", findDep(t, closure, "scoped").Version)

	stale, reason, err := engine.IsStale(man, lock)
	require.NoError(t, err)
	assert.False(t, stale, reason)
}

func TestPathManifestNameMustMatchThePackage(t *testing.T) {
	t.Parallel()

	app, neighbour := pathManifestLayout(t,
		strings.Replace(contribManifest, "name = 'contrib'", "name = 'other'", 1))
	man := parseManifest(t, consumerManifest)

	engine := resolve.NewEngine(contribAdapter(t, neighbour), app, "tt 3.4.0")

	_, _, err := engine.Resolve(context.Background(), man)
	require.ErrorIs(t, err, resolve.ErrPathPackageName)
	assert.Contains(t, err.Error(), `"other"`)
}

// TestPathManifestOfAProduct: a path dependency named after the rock of
// another product of the package is that product, with its dependencies; one
// named after no rock of the package says which names there are.
func TestPathManifestOfAProduct(t *testing.T) {
	t.Parallel()

	contrib := contribManifest + `[components.tools]
path = 'tools'
[components.tools.dependencies]
luasocket = '>=3.0.0'
[products.tools]
components = ['tools']
rock = 'contrib-tools'
`

	app, neighbour := pathManifestLayout(t, contrib)

	engine := resolve.NewEngine(contribAdapter(t, neighbour).add("luasocket", "3.1.0-1", "l"),
		app, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), parseManifest(t, oneProduct+
		"[dependencies]\ncontrib-tools = { source = 'path', path = '../libs/contrib' }\n"))
	require.NoError(t, err)

	closure := lock.Products["default"].Dependencies
	assert.Equal(t, "1.4.0.rc.1-1", findDep(t, closure, "contrib-tools").Version)
	// The dependency of the tools component; [dependencies] are of both.
	assert.Equal(t, "3.1.0-1", findDep(t, closure, "luasocket").Version)
	assert.Equal(t, "1.5.0-1", findDep(t, closure, "metrics").Version)

	_, _, err = engine.Resolve(context.Background(), parseManifest(t, oneProduct+
		"[dependencies]\ncontrib-cli = { source = 'path', path = '../libs/contrib' }\n"))
	require.ErrorIs(t, err, resolve.ErrPathPackageName)
	assert.Contains(t, err.Error(), `name the dependency "contrib" or "contrib-tools"`)
}

// TestPathManifestWinsOverRockspec pins that a manifest beside a rockspec is
// the source of truth, and that the rockspec being ignored is said once per
// run, however many products reach the dependency.
func TestPathManifestWinsOverRockspec(t *testing.T) {
	t.Parallel()

	app, neighbour := pathManifestLayout(t, contribManifest)
	writeTree(t, neighbour, map[string]string{"contrib-scm-1.rockspec": "package = 'contrib'\n"})

	fake := contribAdapter(t, neighbour).
		addLocal(neighbour, "contrib", "scm-1", dep(t, "nowhere", ">=1.0.0"))

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
contrib = { source = 'path', path = '../libs/contrib' }
[components.app]
path = '.'
[products.one]
components = ['app']
default = true
[products.two]
components = ['app']
`)

	engine := resolve.NewEngine(fake, app, "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	for _, product := range []string{"one", "two"} {
		contrib := findDep(t, lock.Products[product].Dependencies, "contrib")
		assert.Equal(t, "1.4.0.rc.1-1", contrib.Version, "the manifest's, not the rockspec's")
		assert.NotContains(t, depNames(lock.Products[product].Dependencies), "nowhere")
	}

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "contrib-scm-1.rockspec")
	assert.Contains(t, warnings[0], "ignores")
}

// TestPathManifestHashIgnoresWhatABuildWrites pins that what a build of the
// neighbour, or of its consumer, leaves in the neighbour's directory does not
// make the consumer's lock stale, while a source does.
func TestPathManifestHashIgnoresWhatABuildWrites(t *testing.T) {
	t.Parallel()

	app, neighbour := pathManifestLayout(t, contribManifest)
	writeTree(t, neighbour, map[string]string{".gitignore": "*.o\n/generated/\n"})

	man := parseManifest(t, consumerManifest)
	engine := resolve.NewEngine(contribAdapter(t, neighbour), app, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	writeTree(t, neighbour, map[string]string{
		"_build/rock/default/lua/contrib/init.lua": "return {}\n",
		".rocks/share/tarantool/metrics/init.lua":  "return {}\n",
		"_runtime/bin/tarantool":                   "binary",
		".git/HEAD":                                "ref: refs/heads/master\n",
		"app.manifest.lock":                        "lock_version = '0.1'\n",
		"contrib/fast.o":                           "object",
		"generated/bindings.lua":                   "return {}\n",
	})

	stale, reason, err := resolve.IsStale(app, man, lock)
	require.NoError(t, err)
	assert.False(t, stale, reason)

	writeTree(t, neighbour, map[string]string{"contrib/init.lua": "return 2\n"})

	stale, reason, err = resolve.IsStale(app, man, lock)
	require.NoError(t, err)
	assert.True(t, stale)
	assert.Contains(t, reason, `"contrib" changed on disk`)
}

// TestPathManifestVersionMoveIsStale pins that the lock follows the version
// git derives for the neighbour: a commit that changes no file the content
// hash covers still moves the version of the rock that gets installed.
func TestPathManifestVersionMoveIsStale(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found in PATH")
	}

	app, neighbour := pathManifestLayout(t, contribManifest)
	require.NoError(t, os.Remove(filepath.Join(neighbour, "VERSION")))

	git := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git",
			append([]string{"-C", neighbour}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}

	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("config", "commit.gpgsign", "false")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	git("tag", "v2.0.0")

	man := parseManifest(t, consumerManifest)
	engine := resolve.NewEngine(contribAdapter(t, neighbour), app, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Equal(t, "2.0.0-1", findDep(t, lock.Products["default"].Dependencies, "contrib").Version)

	stale, reason, err := resolve.IsStale(app, man, lock)
	require.NoError(t, err)
	assert.False(t, stale, reason)

	git("commit", "--allow-empty", "-q", "-m", "next")

	stale, reason, err = resolve.IsStale(app, man, lock)
	require.NoError(t, err)
	assert.True(t, stale)
	assert.Contains(t, reason, "is at version 2.0.1.dev.1.g")
}

// TestPathManifestConsumerDeclarationWins pins that a dependency the consumer
// declares itself keeps the consumer's identity when a neighbour declares it
// by path: the consumer's tree holds one rock of that name.
func TestPathManifestConsumerDeclarationWins(t *testing.T) {
	t.Parallel()

	app, neighbour := pathManifestLayout(t, contribManifest)

	man := parseManifest(t, consumerManifest+`helper = '>=0.1.0'
`)

	fake := contribAdapter(t, neighbour).add("helper", "0.2.0-1", "h")
	engine := resolve.NewEngine(fake, app, "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	helper := findDep(t, lock.Products["default"].Dependencies, "helper")
	assert.Equal(t, manifest.LockDependency{
		Name: "helper", Version: "0.2.0-1", Source: "registry", Checksum: "md5:h",
	}, helper)
}
