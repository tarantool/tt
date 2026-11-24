package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localConfig is the cluster config of two instances: i2 moves to a
// work_dir of its own and logs into a file.
const localConfig = `groups:
  g:
    replicasets:
      r:
        instances:
          i1: {}
          i2:
            process:
              work_dir: /srv/i2
            log:
              to: file
`

// TestMainLocalInstances resolves the instances of a project of this host
// from a module, as tt instance status selects them: none is every instance
// of the project, <app>:* every instance of the application and
// <app>:<instance> one, with where its process runs, its run directory, its
// logs and the crashes its watchdog recorded. A bare application, an
// application or an instance that is not found, and a scope with no
// application fail or answer as for tt instance status. Nothing runs: the
// instances are described by their files.
func TestMainLocalInstances(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "lp")
	writeFiles(t, project, map[string]string{
		".templates/default/instances.yaml": "i1:\ni2:\n",
		".templates/default/config.yaml":    localConfig,
	})

	runDir := filepath.Join(project, ".tarantool", "var", "run", "lp", "i1")
	logDir := filepath.Join(project, ".tarantool", "var", "log", "lp", "i2")

	require.NoError(t, os.MkdirAll(runDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "crashes.json"), []byte(`{"version": 1,
		"crashes": [{"at": "2026-11-24T10:00:00Z", "pid": 42, "signal": 11,
		"core_dumped": true}]}`), 0o644))

	env := []string{"TT_SYSTEM_ROOT=" + t.TempDir(), "TT_USER_ROOT=" + t.TempDir()}

	tt := func(args ...string) result {
		t.Helper()

		got := runTT(t, ttRun{program: "demo", dir: project, env: env,
			args: append([]string{"demo", "instances"}, args...)})
		require.Equal(t, 0, got.code, got.stderr)

		return got
	}

	first := "lp:i1 work=" + project + " run=" + runDir + " log=" +
		filepath.Join(project, ".tarantool", "var", "log", "lp", "i1", "tt.log") +
		" tarantool= crashes=[{2026-11-24 10:00:00 +0000 UTC 42 segmentation fault true}]\n"
	second := "lp:i2 work=/srv/i2 run=" +
		filepath.Join(project, ".tarantool", "var", "run", "lp", "i2") + " log=" +
		filepath.Join(logDir, "tt.log") + " tarantool=" + filepath.Join(logDir, "tarantool.log") +
		" crashes=[]\n"

	assert.Equal(t, first+second, tt().stdout)
	assert.Equal(t, first+second, tt("lp:*").stdout)
	assert.Equal(t, second, tt("lp:i2").stdout)
	// The instances of an application come in its order, each once.
	assert.Equal(t, first+second, tt("lp:i2,lp:i1", "lp:*").stdout)

	for _, target := range []string{"lp:none", "nope:i1"} {
		assert.Regexp(t, "^not found: ", tt(target).stdout, target)
	}

	// The messages sdktest gives a module of the same targets.
	for target, message := range map[string]string{
		"lp": `invalid target "lp": an instance-level command needs an instance target; use ` +
			`lp:<INSTANCE> or lp:*`,
		"lp:": `invalid target "lp:": the instance name is missing; use lp:<INSTANCE> or lp:*`,
		":i1": `invalid target ":i1": the application name is missing; use <APP>:i1`,
	} {
		bare := runTT(t, ttRun{program: "demo", dir: project, env: env,
			args: []string{"demo", "instances", target}})
		assert.NotEqual(t, 0, bare.code, target)
		assert.Contains(t, bare.stderr, message, target)
	}

	// No application is installed in the user scope: no instance.
	assert.Empty(t, tt("--scope", "user").stdout)

	// Crashes a newer tt wrote are of the instance alone: it has none, and
	// why, and the other instance is listed as it was.
	newerRun := filepath.Join(project, ".tarantool", "var", "run", "lp", "i2")
	newer := filepath.Join(newerRun, "crashes.json")

	require.NoError(t, os.MkdirAll(newerRun, 0o755))
	require.NoError(t, os.WriteFile(newer, []byte(`{"version": 2}`), 0o600))
	assert.Equal(t, first+strings.TrimSuffix(second, "\n")+" crashes-error=cannot read the "+
		"crashes of "+newer+": written by a newer tt, of version 2 of the format, and this "+
		"one reads 1\n", tt().stdout)
}
