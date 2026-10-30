//go:build integration

package core_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eeTarantoolEnv names the Tarantool Enterprise executable the tests that
// need one run; without it they look for one in PATH.
const eeTarantoolEnv = "TT_TEST_TARANTOOL_EE"

// eeVersion takes the version out of the banner of Tarantool Enterprise.
var eeVersion = regexp.MustCompile(`^Tarantool Enterprise (\d+\.\d+\.\d+)`)

// eeTarantool returns the Tarantool Enterprise executable and its version,
// and skips the test when there is none.
func eeTarantool(t *testing.T) (string, string) {
	t.Helper()

	exe := os.Getenv(eeTarantoolEnv)
	if exe == "" {
		var err error

		exe, err = exec.LookPath("tarantool")
		if err != nil {
			t.Skip("tarantool is not found")
		}
	}

	//nolint:gosec // The tarantool the test is told to run.
	out, err := exec.CommandContext(t.Context(), exe, "--version").Output()
	require.NoError(t, err)

	match := eeVersion.FindSubmatch(out)
	if match == nil {
		t.Skipf("%s is not Tarantool Enterprise; set %s", exe, eeTarantoolEnv)
	}

	return exe, string(match[1])
}

// eeSignedManifest is signedManifest on Tarantool Enterprise.
var eeSignedManifest = strings.Replace(signedManifest, `tarantool = '>=3.0.0,<4.0.0'`,
	`tarantool = '>=3.0.0,<4.0.0[ee]'`, 1)

// eeInitLua is the application module: it creates a space the test looks
// for over the binary port.
const eeInitLua = "box.schema.space.create('s', {if_not_exists = true})\n"

// eeLayoutConfig is a config that puts every file its instances write into
// the layout of the scope root root, as the README of tt shows for
// Tarantool Enterprise, which drops the environment tt passes the layout
// through when it checks integrity.
func eeLayoutConfig(root string) string {
	data := root + "/var/lib/signed/{{ instance_name }}"
	run := root + "/var/run/signed/{{ instance_name }}"

	return "app:\n  module: signed\n" +
		"snapshot:\n  dir: " + data + "\n" +
		"wal:\n  dir: " + data + "\n" +
		"vinyl:\n  dir: " + data + "\n" +
		"quiver:\n  dir: " + data + "\n" +
		"process:\n  pid_file: " + run + "/tarantool.pid\n" +
		"console:\n  socket: " + run + "/tarantool.control\n" +
		"iproto:\n  listen:\n  - uri: unix/:" + run + "/tarantool.sock\n" +
		"credentials:\n  users:\n    client:\n      password: secret\n" +
		"      roles: [super]\n" +
		signedConfig
}

// installedRun runs exe, a tt installed as a signed runtime, as the
// integrity program with the environment env, and returns how it ended.
func installedRun(t *testing.T, exe string, env []string, stdin string, args ...string) result {
	t.Helper()

	// Not the context of the test: the instance is stopped in a cleanup,
	// after the context of the test is done.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	process := exec.CommandContext(ctx, exe, args...)

	process.Dir = t.TempDir()
	process.Stdin = strings.NewReader(stdin)

	process.Env = append(append(os.Environ(), env...), mainCaseEnv+"=integrity")

	var stdout, stderr bytes.Buffer

	process.Stdout = &stdout
	process.Stderr = &stderr

	code := 0

	err := process.Run()
	if exitErr := (*exec.ExitError)(nil); err != nil {
		require.ErrorAs(t, err, &exitErr)

		code = exitErr.ExitCode()
	}

	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// TestIntegrityEnterpriseLayout packs a package for Tarantool Enterprise
// whose config puts the files of its instance into the layout of the user
// root, installs it signed, and runs the instance with the checks on.
// Tarantool Enterprise starts with an empty environment then, so the layout
// comes from the config alone: the data, the pid file and the sockets are
// where it says, not in the version, and tt finds them - status, connect over
// the console socket and over the binary port, logrotate and stop work. A
// changed file of the version is still refused.
func TestIntegrityEnterpriseLayout(t *testing.T) {
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
		"init.lua":                          eeInitLua,
		".templates/default/instances.yaml": "storage-1:\n",
		".templates/default/config.yaml":    eeLayoutConfig(root),
	})

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

	got = runTT(t, ttRun{
		program: "integrity", env: env,
		args: []string{
			"--integrity-check", public, "package", "install", archive, "--scope", "user",
		},
	})
	require.Equal(t, 0, got.code, got.stderr)

	versionDir := filepath.Join(root, "versions", "signed", "signed_1.0.0")
	installedTt := filepath.Join(root, "versions", "tt", "tt_3.1.0", "tt")
	target := "signed:storage-1"
	checks := []string{"--integrity-check", public}

	tt := func(stdin string, args ...string) result {
		t.Helper()

		return installedRun(t, installedTt, env, stdin,
			append(append(append([]string{}, checks...), args...), "--scope", "user")...)
	}

	got = tt("", "instance", "start", target)
	require.Equal(t, 0, got.code, got.stderr)

	t.Cleanup(func() {
		tt("", "instance", "stop", target, "--yes")

		if t.Failed() {
			log, _ := os.ReadFile(filepath.Join(root, "var", "log", "signed", "storage-1",
				"tt.log"))
			t.Logf("tt.log:\n%s", log)
		}
	})

	data := filepath.Join(root, "var", "lib", "signed", "storage-1")
	run := filepath.Join(root, "var", "run", "signed", "storage-1")

	require.Eventually(t, func() bool {
		got := tt("return box.info.status\n", "connect", target, "-f", "-")

		return got.code == 0 && strings.Contains(got.stdout, "running")
	}, 30*time.Second, 200*time.Millisecond, "the instance is not running")

	snaps, err := filepath.Glob(filepath.Join(data, "*.snap"))
	require.NoError(t, err)
	assert.NotEmpty(t, snaps, "no snapshot in %s", data)
	assert.FileExists(t, filepath.Join(run, "tarantool.pid"))
	assert.FileExists(t, filepath.Join(run, "tarantool.sock"))
	requireVarLinks(t, root, versionDir)

	got = tt("", "instance", "status", target)
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "RUNNING")

	got = tt("return box.info.status\n", "connect", target, "-f", "-")
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "running")

	got = tt("return box.space.s ~= nil\n", "connect", target, "--binary",
		"-u", "client", "-p", "secret", "-f", "-")
	require.Equal(t, 0, got.code, got.stderr)
	assert.Contains(t, got.stdout, "true")

	got = tt("", "instance", "logrotate", target)
	require.Equal(t, 0, got.code, got.stderr)

	got = tt("", "instance", "stop", target, "--yes")
	require.Equal(t, 0, got.code, got.stderr)

	// A changed file of the version is refused before anything starts.
	initLua := filepath.Join(versionDir, ".rocks", "share", "tarantool", "signed", "init.lua")
	require.NoError(t, os.WriteFile(initLua, []byte("os.exit(1)\n"), 0o644))

	got = tt("", "instance", "start", target)
	require.NotEqual(t, 0, got.code)
	assert.Contains(t, got.stderr, "hash mismatch")
}
