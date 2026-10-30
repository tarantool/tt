//go:build integration

package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	etcdtest "github.com/tarantool/go-storage/v2/test_helpers/etcd"
)

// eeDataInitLua is the application module: a space with a primary key, so
// that the test can store a tuple and look for it after an upgrade.
const eeDataInitLua = "box.once('schema', function()\n" +
	"  box.schema.space.create('s')\n" +
	"  box.space.s:create_index('pk')\n" +
	"end)\n"

// eeDefaultsConfig is a config that names no path: the instance finds the
// layout of the scope only by the defaults of Tarantool.
const eeDefaultsConfig = "app:\n  module: signed\n" + signedConfig

// signedScenario is a signed package of the application signed, installed
// into the user root of a test, with the tt and the Tarantool Enterprise it
// bundles.
type signedScenario struct {
	t       *testing.T
	project string
	root    string
	env     []string
	private string
	public  string
}

// newSignedScenario makes the project of the package, with config as its
// cluster config, and the runtimes it bundles.
func newSignedScenario(t *testing.T, config string) *signedScenario {
	t.Helper()

	tarantool, version := eeTarantool(t)
	private, public := opensslKeys(t)

	cache := t.TempDir()
	copyExecutable(t, tarantool,
		filepath.Join(cache, "tt", "runtimes", "tarantool", "ee", version, "bin", "tarantool"))
	copyExecutable(t, os.Args[0],
		filepath.Join(cache, "tt", "runtimes", "tt", "ce", "3.1.0", "bin", "tt"))

	// The sockets of the instance are bound by the full path, which the
	// root of a temporary directory leaves room for.
	root := filepath.Join(t.TempDir(), "user")

	project := t.TempDir()
	writeFiles(t, project, map[string]string{
		"app.manifest.toml":                 eeSignedManifest,
		"VERSION":                           "1.0.0\n",
		"init.lua":                          eeDataInitLua,
		".templates/default/instances.yaml": "storage-1:\n",
		".templates/default/config.yaml":    config,
	})

	return &signedScenario{
		t: t, project: project, root: root, private: private, public: public,
		env: []string{
			"XDG_CACHE_HOME=" + cache,
			"TT_USER_ROOT=" + root,
			"PATH=" + filepath.Dir(tarantool) + string(os.PathListSeparator) + os.Getenv("PATH"),
		},
	}
}

// install packs version ver of the package, signed, and installs it into the
// user scope with the checks on.
func (s *signedScenario) install(ver string) {
	s.t.Helper()

	writeFiles(s.t, s.project, map[string]string{"VERSION": ver + "\n"})

	got := runTT(s.t, ttRun{
		program: "integrity", dir: s.project, env: s.env,
		args: []string{"package", "pack", "--with-integrity-check", s.private},
	})
	require.Equal(s.t, 0, got.code, got.stderr)

	got = runTT(s.t, ttRun{
		program: "integrity", env: s.env,
		args: []string{
			"--integrity-check", s.public, "package", "install", strings.TrimSpace(got.stdout),
			"--scope", "user",
		},
	})
	require.Equal(s.t, 0, got.code, got.stderr)
}

// tt runs the installed tt with the checks on, in the user scope.
func (s *signedScenario) tt(stdin string, args ...string) result {
	s.t.Helper()

	installedTt := filepath.Join(s.root, "versions", "tt", "tt_3.1.0", "tt")

	return installedRun(s.t, installedTt, s.env, stdin,
		append(append([]string{"--integrity-check", s.public}, args...), "--scope", "user")...)
}

// versionDir is the directory of version ver of the package.
func (s *signedScenario) versionDir(ver string) string {
	return filepath.Join(s.root, "versions", "signed", "signed_"+ver)
}

// layoutDir is the directory of the layout of storage-1 of kind lib, run or
// log.
func (s *signedScenario) layoutDir(kind string) string {
	return filepath.Join(s.root, "var", kind, "signed", "storage-1")
}

// requireLinks checks that var/ of version ver holds nothing but the links
// to the layout of the application.
func (s *signedScenario) requireLinks(ver string) {
	s.t.Helper()

	requireVarLinks(s.t, s.root, s.versionDir(ver))
}

// requireVarLinks checks that var/ of the version in versionDir of the
// application signed, installed under root, holds nothing but the links to
// the layout of the application: the instances wrote nothing into the
// version.
func requireVarLinks(t *testing.T, root, versionDir string) {
	t.Helper()

	varDir := filepath.Join(versionDir, "var")

	entries, err := os.ReadDir(varDir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
		assert.Equal(t, os.ModeSymlink, entry.Type()&os.ModeSymlink,
			"%s is not a link", filepath.Join(varDir, entry.Name()))
	}

	assert.ElementsMatch(t, []string{"lib", "log", "run"}, names)

	// The links lead to the layout before it exists: tt creates the
	// directories of an instance when it starts it. So they are read, not
	// resolved, from the directory they are in.
	realVar, err := filepath.EvalSymlinks(varDir)
	require.NoError(t, err)

	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	for _, kind := range []string{"lib", "log", "run"} {
		target, err := os.Readlink(filepath.Join(varDir, kind))
		require.NoError(t, err)

		assert.False(t, filepath.IsAbs(target), "var/%s of %s is absolute: %s", kind,
			versionDir, target)
		assert.Equal(t, filepath.Join(realRoot, "var", kind, "signed"),
			filepath.Join(realVar, target), "var/%s of %s", kind, versionDir)
	}
}

// waitRunning waits until storage-1 answers over its console socket.
func (s *signedScenario) waitRunning() {
	s.t.Helper()

	require.Eventually(s.t, func() bool {
		got := s.tt("return box.info.status\n", "connect", "signed:storage-1", "-f", "-")

		return got.code == 0 && strings.Contains(got.stdout, "running")
	}, 30*time.Second, 200*time.Millisecond, "the instance is not running")
}

// TestIntegrityEnterpriseDefaultLayout installs a signed package whose config
// names no path, and runs its instance with the integrity checks on.
// Tarantool Enterprise starts with an empty environment then and puts its
// files where its defaults say, under the version; the version links those
// directories to the layout of the root, so the data, the pid file and the
// console socket are in the layout, and tt finds them: status, connect,
// logrotate and stop work. A newer version installed next to it links the
// same layout, and the instance restarted from it has its data. A changed
// file of the version is still refused, and an uninstall removes the
// versions and keeps the data.
func TestIntegrityEnterpriseDefaultLayout(t *testing.T) {
	pkg := newSignedScenario(t, eeDefaultsConfig)
	target := "signed:storage-1"

	pkg.install("1.0.0")
	pkg.requireLinks("1.0.0")

	got := pkg.tt("", "instance", "start", target)
	require.Equal(t, 0, got.code, got.stderr)

	t.Cleanup(func() {
		pkg.tt("", "instance", "stop", target, "--yes")

		if t.Failed() {
			log, _ := os.ReadFile(filepath.Join(pkg.layoutDir("log"), "tt.log"))
			t.Logf("tt.log:\n%s", log)
		}
	})

	pkg.waitRunning()

	snaps, err := filepath.Glob(filepath.Join(pkg.layoutDir("lib"), "*.snap"))
	require.NoError(t, err)
	assert.NotEmpty(t, snaps, "no snapshot in %s", pkg.layoutDir("lib"))
	assert.FileExists(t, filepath.Join(pkg.layoutDir("run"), "tarantool.pid"))
	assert.FileExists(t, filepath.Join(pkg.layoutDir("run"), "tarantool.control"))
	pkg.requireLinks("1.0.0")

	got = pkg.tt("", "instance", "status", target)
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "RUNNING")

	got = pkg.tt("box.space.s:insert{1, 'kept'}\nreturn box.snapshot()\n",
		"connect", target, "-f", "-")
	require.Equal(t, 0, got.code, got.stderr)

	got = pkg.tt("", "instance", "logrotate", target)
	require.Equal(t, 0, got.code, got.stderr)

	got = pkg.tt("", "instance", "stop", target, "--yes")
	require.Equal(t, 0, got.code, got.stderr)

	// An upgrade: the new version links the same layout, and the instance
	// started from it has the data the old one wrote.
	pkg.install("1.1.0")
	pkg.requireLinks("1.1.0")

	got = pkg.tt("", "instance", "start", target)
	require.Equal(t, 0, got.code, got.stderr)

	pkg.waitRunning()

	got = pkg.tt("return box.space.s:get{1}[2]\n", "connect", target, "-f", "-")
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "kept")

	got = pkg.tt("", "instance", "stop", target, "--yes")
	require.Equal(t, 0, got.code, got.stderr)

	// A changed file of the version is refused before anything starts.
	initLua := filepath.Join(pkg.versionDir("1.1.0"), ".rocks", "share", "tarantool", "signed",
		"init.lua")
	require.NoError(t, os.WriteFile(initLua, []byte("os.exit(1)\n"), 0o644))

	got = pkg.tt("", "instance", "start", target)
	require.NotEqual(t, 0, got.code)
	assert.Contains(t, got.stderr, "hash mismatch")

	// An uninstall removes the versions and the links, not what they lead to.
	got = runTT(t, ttRun{
		program: "integrity", env: pkg.env,
		args: []string{"package", "uninstall", "signed", "--scope", "user", "--yes"},
	})
	require.Equal(t, 0, got.code, got.stderr)
	assert.NoDirExists(t, pkg.versionDir("1.0.0"))
	assert.NoDirExists(t, pkg.versionDir("1.1.0"))

	kept, err := filepath.Glob(filepath.Join(pkg.layoutDir("lib"), "*.snap"))
	require.NoError(t, err)
	assert.NotEmpty(t, kept, "the uninstall removed the data in %s", pkg.layoutDir("lib"))
}

// TestIntegrityEnterpriseDefaultLayoutEtcd is TestIntegrityEnterpriseDefaultLayout
// with the cluster config in etcd: the package holds only the config that
// names the storage, the config published there with a signature names no
// path, and the instance still finds the layout through the links of its
// version.
func TestIntegrityEnterpriseDefaultLayoutEtcd(t *testing.T) {
	etcd := etcdtest.NewLazyCluster(etcdtest.ClusterConfig{Size: 1, PeerTLS: nil})
	t.Cleanup(etcd.Terminate)

	endpoint := etcd.EndpointsHTTP()[0]

	pkg := newSignedScenario(t, "config:\n  etcd:\n    endpoints: ["+endpoint+"]\n"+
		"    prefix: /signed\n")
	target := "signed:storage-1"

	published := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(published, []byte(eeDefaultsConfig), 0o644))

	got := runTT(t, ttRun{
		program: "integrity", env: pkg.env,
		args: []string{
			"cluster", "publish", "--with-integrity-check", pkg.private, endpoint + "/signed",
			published,
		},
	})
	require.Equal(t, 0, got.code, got.stderr)

	pkg.install("1.0.0")
	pkg.requireLinks("1.0.0")

	got = pkg.tt("", "instance", "start", target)
	require.Equal(t, 0, got.code, got.stderr)

	t.Cleanup(func() {
		pkg.tt("", "instance", "stop", target, "--yes")

		if t.Failed() {
			log, _ := os.ReadFile(filepath.Join(pkg.layoutDir("log"), "tt.log"))
			t.Logf("tt.log:\n%s", log)
		}
	})

	pkg.waitRunning()

	snaps, err := filepath.Glob(filepath.Join(pkg.layoutDir("lib"), "*.snap"))
	require.NoError(t, err)
	assert.NotEmpty(t, snaps, "no snapshot in %s", pkg.layoutDir("lib"))
	assert.FileExists(t, filepath.Join(pkg.layoutDir("run"), "tarantool.control"))
	pkg.requireLinks("1.0.0")

	got = pkg.tt("", "instance", "status", target)
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "RUNNING")

	got = pkg.tt("", "instance", "stop", target, "--yes")
	require.Equal(t, 0, got.code, got.stderr)
}
