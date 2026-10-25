package run_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/install"
	"github.com/tarantool/tt/v3/cli/manifest/pack"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/run"
)

// smokeManifest is a dependency-free pure-Lua package, packed with its runtime.
const smokeManifest = `manifest_version = '0.1'

[package]
name = 'smoke-app'

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

// deployManifest makes the deploy directory a project of its own, as tt run
// requires, with smoke-app installed into it as a guest.
const deployManifest = `manifest_version = '0.1'

[package]
name = 'deploy'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'
`

// hostTarantool returns the tarantool on PATH and the version it reports, or
// skips the test when there is none: the smoke test runs the real binary.
func hostTarantool(t *testing.T) (string, string) {
	t.Helper()

	path, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("no tarantool on PATH")
	}

	out, err := exec.CommandContext(t.Context(), path, "--version").Output()
	require.NoError(t, err)

	fields := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
	require.GreaterOrEqual(t, len(fields), 2, "unexpected banner %q", out)

	return path, fields[len(fields)-1]
}

// TestRunInstalledProjectUsesBundledTarantool is the smoke test of tt run on
// an installed package: the host tarantool is packed into a with-deps archive
// by the real pack, the archive is installed into a project by the real
// install, and the interpreter tt run chooses there is the bundled one — with
// no tarantool left on PATH — and runs the package from the project's .rocks/
// once started in the project root, as run.Exec starts it.
//
// Not parallel: PATH is process-wide.
func TestRunInstalledProjectUsesBundledTarantool(t *testing.T) {
	host, hostVersion := hostTarantool(t)

	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	src := t.TempDir()
	writeSource(t, src, map[string]string{
		"app.manifest.toml": smokeManifest,
		"VERSION":           "1.0.0\n",
		"init.lua":          "return 'smoke-app from .rocks'\n",
	})

	fakeTt := filepath.Join(t.TempDir(), "tt")
	writeExecutable(t, fakeTt)

	packed, err := pack.Run(context.Background(), pack.Options{
		ProjectDir: src,
		OutputDir:  filepath.Join(src, "_out"),
		Build: build.Options{
			TtVersion: "tt test",
			Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: hostVersion},
		},
		Runtime: pack.RuntimeOptions{
			ActiveTarantool:        host,
			ActiveTarantoolVersion: hostVersion,
			ActiveTarantoolFlavor:  "ce",
			ActiveTt:               fakeTt,
			ActiveTtVersion:        "3.2.0",
		},
	})
	require.NoError(t, err)

	// A Community build of a release tag is locked as the release (decision
	// 26), and the bundled runtime is installed under that name.
	locked := hostVersion
	if release, _, onTag := strings.Cut(hostVersion, "-0-g"); onTag {
		locked = release
	}

	require.Equal(t, locked, packed.Bundled.Tarantool)

	deploy := t.TempDir()
	writeSource(t, deploy, map[string]string{"app.manifest.toml": deployManifest})

	_, err = install.Run(context.Background(), install.Options{
		ProjectDir: deploy,
		Scope:      install.ScopeProject,
		Archives:   []string{packed.Path},
		Yes:        true,
	})
	require.NoError(t, err)

	// Nothing but the project's own selection can answer now.
	t.Setenv("PATH", "")

	root, err := run.ProjectRoot(deploy)
	require.NoError(t, err)

	exe, err := run.Tarantool(root)
	require.NoError(t, err)

	bundled := filepath.Join(deploy, ".tarantool", "versions", "tarantool",
		"tarantool_"+locked, "tarantool")
	assert.Equal(t, bundled, exe.Path)
	assert.NoDirExists(t, filepath.Join(deploy, "_runtime"))

	script := exec.CommandContext(t.Context(), exe.Path, "-e",
		"print(_TARANTOOL) print(require('smoke-app')) os.exit(0)")

	script.Dir = root

	out, err := script.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Equal(t, hostVersion+"\nsmoke-app from .rocks\n", string(out))
}

// writeSource writes a set of files into dir, creating parents.
func writeSource(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), dirMode))
		require.NoError(t, os.WriteFile(path, []byte(content), fileMode))
	}
}
