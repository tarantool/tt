package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/running"
)

// appClusterYAML is the cluster configuration of the test application: two
// instances in one replicaset, the mode set for the replicaset and
// overridden by one instance.
const appClusterYAML = `
groups:
  group-001:
    replicasets:
      replicaset-001:
        database:
          mode: ro
        instances:
          master:
            database:
              mode: rw
          storage: {}
`

// writeFiles writes files, relative paths to contents, under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, name)

		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

// TestSplitInstanceDelimiter checks that the SDK splits an instance
// reference at the delimiter tt's commands use.
func TestSplitInstanceDelimiter(t *testing.T) {
	t.Parallel()

	app, instance := sdk.SplitInstance("app" + string(running.InstanceDelimiter) + "inst")
	assert.Equal(t, "app", app)
	assert.Equal(t, "inst", instance)
}

// TestMainClusterConfig checks Services.ClusterConfig on the sources a
// command line names: an application of the tt environment and a file.
func TestMainClusterConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	writeFiles(t, dir, map[string]string{
		"myapp/tt.yaml":       "",
		"myapp/config.yaml":   appClusterYAML,
		"myapp/instances.yml": "master:\nstorage:\n",
		"plain/tt.yaml":       "",
		"plain/init.lua":      "return 1\n",
	})

	inEnv := func(env string, args ...string) []string {
		return append([]string{"--cfg", filepath.Join(dir, env, "tt.yaml"), "demo", "cluster"},
			args...)
	}

	for _, testCase := range []struct {
		name   string
		args   []string
		stdout string
	}{
		{
			name: "application", args: inEnv("myapp", "myapp", "storage"),
			stdout: "master storage\nmode=ro\n",
		},
		{
			name: "application instance override", args: inEnv("myapp", "myapp", "master"),
			stdout: "master storage\nmode=rw\n",
		},
		{
			name: "unknown instance", args: inEnv("myapp", "myapp", "nosuch"),
			stdout: "master storage\nnot found: instance \"nosuch\" not found\n",
		},
		{
			name: "unknown application", args: inEnv("myapp", "nosuch"),
			stdout: "not found: cluster configuration of application \"nosuch\": " +
				"can't collect instance information for nosuch: application \"nosuch\" " +
				"not found\n",
		},
		{
			name: "application without a cluster configuration", args: inEnv("plain", "plain"),
			stdout: "not found: application \"plain\": no cluster configuration\n",
		},
		{
			name: "no tt environment", args: []string{"demo", "cluster", "myapp"},
			stdout: "not found: cluster configuration of application \"myapp\": " +
				"no tt environment\n",
		},
		{
			name: "file", args: []string{
				"demo", "cluster", filepath.Join(dir, "myapp", "config.yaml"), "storage",
			},
			stdout: "master storage\nmode=ro\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := runTT(t, ttRun{program: "demo", args: testCase.args, dir: t.TempDir()})

			require.Equal(t, 0, got.code, got.stderr)
			assert.Equal(t, testCase.stdout, got.stdout)
		})
	}
}

// TestMainClusterConfigDir checks that Services.ClusterConfig reports the
// directory of the cluster config file an application or a file source was
// read from, the directory relative paths in it are relative to.
func TestMainClusterConfigDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	writeFiles(t, dir, map[string]string{
		"work/myapp/tt.yaml":       "",
		"work/myapp/config.yaml":   appClusterYAML,
		"work/myapp/instances.yml": "master:\nstorage:\n",
		"elsewhere/cluster.yaml":   appClusterYAML,
	})

	want := func(path string) string {
		t.Helper()

		resolved, err := filepath.EvalSymlinks(path)
		require.NoError(t, err)

		return resolved
	}

	for _, testCase := range []struct {
		name string
		args []string
		dir  string
	}{
		{
			name: "application",
			args: []string{
				"--cfg", filepath.Join(dir, "work", "myapp", "tt.yaml"),
				"demo", "cluster-dir", "myapp",
			},
			dir: want(filepath.Join(dir, "work", "myapp")),
		},
		{
			name: "file",
			args: []string{"demo", "cluster-dir", filepath.Join(dir, "elsewhere", "cluster.yaml")},
			dir:  want(filepath.Join(dir, "elsewhere")),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := runTT(t, ttRun{program: "demo", args: testCase.args, dir: t.TempDir()})
			require.Equal(t, 0, got.code, got.stderr)

			reported := strings.TrimSpace(got.stdout)
			require.True(t, filepath.IsAbs(reported), reported)

			resolved := want(reported)
			assert.Equal(t, testCase.dir, resolved)
		})
	}
}
