//go:build integration

package core_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectManifest is a package of a Lua component whose template runs one
// Tarantool instance, i1.
const connectManifest = `manifest_version = '0.1'

[package]
name = 'ca'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.lua]
path = '.'
include = ['*.lua']
`

const connectConfig = `groups:
  g:
    replicasets:
      r:
        instances:
          i1: {}
`

// TestMainConnectInstance starts an instance of a project with tt instance
// start and runs Lua on it from a module, by its app:inst target, as tt
// connect reaches it: at its console socket, as the admin. A bare
// application, credentials, an application or an instance that is not
// found, and the instance once it is stopped fail as they fail for tt
// connect.
func TestMainConnectInstance(t *testing.T) {
	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found")
	}

	// The control socket is bound by its full path, under the project.
	//nolint:usetesting // The path of t.TempDir() may be too long for a socket.
	project, err := os.MkdirTemp("", "c")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(project) })

	writeFiles(t, project, map[string]string{
		"app.manifest.toml":                 connectManifest,
		"init.lua":                          "return {}\n",
		".templates/default/instances.yaml": "i1:\n",
		".templates/default/config.yaml":    connectConfig,
	})

	env := []string{
		"TT_SYSTEM_ROOT=" + t.TempDir(), "TT_USER_ROOT=" + t.TempDir(),
		"PATH=" + filepath.Dir(tarantool) + string(os.PathListSeparator) + os.Getenv("PATH"),
	}

	tt := func(args ...string) result {
		t.Helper()

		return runTT(t, ttRun{program: "demo", dir: project, env: env, args: args})
	}

	got := tt("instance", "start", "ca:i1")
	require.Equal(t, 0, got.code, got.stderr)

	t.Cleanup(func() {
		// Not the context of the test, which is done before the cleanups.
		stop := exec.CommandContext(context.Background(), os.Args[0], //nolint:gosec // Itself.
			"instance", "stop", "ca:i1", "--yes")

		stop.Dir = project

		stop.Env = append(append(os.Environ(), env...), mainCaseEnv+"=demo")

		var out bytes.Buffer

		stop.Stdout, stop.Stderr = &out, &out
		_ = stop.Run()

		if t.Failed() {
			log, _ := os.ReadFile(filepath.Join(project, ".tarantool", "var", "log", "ca", "i1",
				"tt.log"))
			t.Logf("tt instance stop:\n%s\ntt.log:\n%s", out.String(), log)
		}
	})

	require.Eventually(t, func() bool {
		got := tt("demo", "eval", "ca:i1", "return box.info.status")

		return got.code == 0 && got.stdout == "[running]\n"
	}, 30*time.Second, 200*time.Millisecond, "the instance is not running")

	for _, testCase := range []struct {
		name   string
		args   []string
		stdout string
	}{
		{"admin", []string{"ca:i1", "return box.session.user(), ...", "x"}, "[admin x]\n"},
		{"comments", []string{"ca:i1", "local x = 1 -- the result\nreturn x, nil -- and a nil"},
			"[1 <nil>]\n"},
		{"lua error", []string{"ca:i1", "error('boom')"}, "lua error: boom\n"},
		{"the only instance", []string{"ca:*", "return 1"}, "[1]\n"},
		{"project scope", []string{"ca:i1", "return 2", "--scope", "project"}, "[2]\n"},
		{"timeout flag", []string{"ca:i1", "return 3", "--connect-timeout", "5s"}, "[3]\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := tt(append([]string{"demo", "eval"}, testCase.args...)...)
			require.Equal(t, 0, got.code, got.stderr)
			assert.Equal(t, testCase.stdout, got.stdout)
		})
	}

	for _, testCase := range []struct {
		name   string
		args   []string
		stdout string
	}{
		{"unknown application", []string{"nosuch:i1"}, `not found: application not found: ` +
			`"nosuch" is not installed in the system scope`},
		{"unknown instance", []string{"ca:i2"}, `not found: instance not found: "i2" of "ca"`},
		{"another scope", []string{"ca:i1", "--scope", "user"}, `not found: application not ` +
			`found: "ca" is not installed in the user scope`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := tt(append([]string{"demo", "eval", testCase.args[0], "return 1"},
				testCase.args[1:]...)...)
			require.Equal(t, 0, got.code, got.stderr)
			assert.True(t, strings.HasPrefix(got.stdout, testCase.stdout), got.stdout)
		})
	}

	for _, testCase := range []struct {
		name   string
		args   []string
		stderr string
	}{
		{"bare application", []string{"ca"}, `invalid target "ca": an instance-level command ` +
			`needs an instance target; use ca:<INSTANCE> or ca:*`},
		{"credentials", []string{"ca:i1", "-u", "admin", "-p", "secret"},
			"username and password are not supported with a connection via a control socket"},
		{"unknown authentication method", []string{"ca:i1", "--auth-type", "md5"},
			`invalid argument "md5" for "--auth-type" flag: unknown authentication method "md5"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := tt(append([]string{"demo", "eval", testCase.args[0], "return 1"},
				testCase.args[1:]...)...)
			assert.Equal(t, 1, got.code)
			assert.Contains(t, got.stderr, testCase.stderr)
		})
	}

	got = tt("instance", "stop", "ca:i1", "--yes")
	require.Equal(t, 0, got.code, got.stderr)

	socket := filepath.Join(project, ".tarantool", "var", "run", "ca", "i1", "tarantool.control")

	got = tt("demo", "eval", "ca:i1", "return 1")
	assert.Equal(t, 1, got.code)
	assert.Contains(t, got.stderr, "instance ca:i1 is not running (no control socket at "+
		socket+"); start it with tt instance start ca:i1")
}
