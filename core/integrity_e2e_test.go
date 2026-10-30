//go:build integration

package core_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signedManifest is a dependency-free package with a template that runs one
// Tarantool instance.
const signedManifest = `manifest_version = '0.1'

[package]
name = 'signed'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.0.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
include = ['*.lua']
`

const signedConfig = `groups:
  g:
    replicasets:
      r:
        instances:
          storage-1: {}
`

// opensslKeys makes an RSA key pair with openssl, in the formats the RSA
// provider reads, and returns the paths of its private and public halves.
func opensslKeys(t *testing.T) (string, string) {
	t.Helper()

	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl is not found")
	}

	dir := t.TempDir()
	private := filepath.Join(dir, "private.pem")
	public := filepath.Join(dir, "public.pem")

	for _, args := range [][]string{
		{"genrsa", "-traditional", "-out", private, "2048"},
		{"rsa", "-in", private, "-pubout", "-out", public},
	} {
		out, err := exec.CommandContext(t.Context(), openssl, args...).CombinedOutput()
		require.NoError(t, err, string(out))
	}

	return private, public
}

// copyExecutable copies the file at src to dst with the mode of a program.
func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()

	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	require.NoError(t, os.WriteFile(dst, data, 0o755))
}

// TestIntegritySignedPackage packs a package signed with an openssl key,
// installs it into the user scope with the public key, and starts its
// instance with the checks on: tt checks what it reads and runs - the
// version, the tarantool and the tt it bundles - and refuses a changed file.
// The bundled tt is this test binary, which runs as the distribution with
// the RSA provider; whether Tarantool itself accepts --integrity-check is
// for Tarantool Enterprise to say, and is not checked here.
func TestIntegritySignedPackage(t *testing.T) {
	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found")
	}

	private, public := opensslKeys(t)

	// The runtime the package bundles comes from the runtime cache: the
	// tarantool of PATH as 3.8.1, and this binary as tt 3.1.0.
	cache := t.TempDir()
	copyExecutable(t, tarantool,
		filepath.Join(cache, "tt", "runtimes", "tarantool", "ce", "3.8.1", "bin", "tarantool"))
	copyExecutable(t, os.Args[0],
		filepath.Join(cache, "tt", "runtimes", "tt", "ce", "3.1.0", "bin", "tt"))

	project := t.TempDir()
	writeFiles(t, project, map[string]string{
		"app.manifest.toml":                 signedManifest,
		"VERSION":                           "1.0.0\n",
		"init.lua":                          "return 1\n",
		".templates/default/instances.yaml": "storage-1:\n",
		".templates/default/config.yaml":    signedConfig,
	})

	root := filepath.Join(t.TempDir(), "user")
	env := []string{
		"XDG_CACHE_HOME=" + cache,
		"TT_USER_ROOT=" + root,
		"PATH=" + filepath.Dir(tarantool) + string(os.PathListSeparator) + os.Getenv("PATH"),
	}

	got := runTT(t, ttRun{
		program: "integrity", dir: project, env: env,
		args: []string{"package", "pack", "--with-integrity-check", private},
	})
	require.Equal(t, 0, got.code, got.stderr)

	archive := strings.TrimSpace(got.stdout)
	require.FileExists(t, archive)

	got = runTT(t, ttRun{
		program: "integrity", env: env,
		args: []string{"--integrity-check", public, "package", "install", archive},
	})
	require.NotEqual(t, 0, got.code)
	assert.Contains(t, got.stderr, "installed into the user or the system scope only")

	got = runTT(t, ttRun{
		program: "integrity", env: env,
		args: []string{
			"--integrity-check", public, "package", "install", archive, "--scope", "user",
		},
	})
	require.Equal(t, 0, got.code, got.stderr)

	version := filepath.Join(root, "versions", "signed", "signed_1.0.0")
	installedTt := filepath.Join(root, "versions", "tt", "tt_3.1.0", "tt")

	require.FileExists(t, filepath.Join(version, "hashes.json"))
	require.FileExists(t, filepath.Join(version, "env_hashes.json"))
	require.FileExists(t, filepath.Join(root, "versions", "tarantool", "tarantool_3.8.1",
		"env_hashes.json"))
	require.FileExists(t, filepath.Join(filepath.Dir(installedTt), "env_hashes.json"))

	// tt from outside the signed runtimes does not start the watchdog.
	got = runTT(t, ttRun{
		program: "integrity", env: env,
		args: []string{
			"--integrity-check", public, "instance", "start", "signed:storage-1",
			"--scope", "user",
		},
	})
	require.NotEqual(t, 0, got.code)
	assert.Contains(t, got.stderr, "is not signed")

	// A changed config of the installed version is refused before anything
	// starts, wherever tt runs and whichever tt it is.
	config := filepath.Join(version, ".templates", "default", "config.yaml")
	require.NoError(t, os.WriteFile(config, []byte(signedConfig+"\n"), 0o644))

	start := exec.CommandContext(t.Context(), installedTt, "--integrity-check", public,
		"instance", "start", "signed:storage-1", "--scope", "user")

	start.Dir = t.TempDir()

	start.Env = append(append(os.Environ(), env...), mainCaseEnv+"=integrity")

	out, err := start.CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), "hash mismatch")
	assert.Contains(t, string(out), config)
}
