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
// command line names: an application, found as the instance commands find
// it, and a file.
func TestMainClusterConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	system := filepath.Join(dir, "system")

	manifest := func(name string) string {
		return "manifest_version = \"0.1\"\n\n[package]\nname = \"" + name + "\"\n"
	}

	writeFiles(t, dir, map[string]string{
		"myapp/app.manifest.toml":                 manifest("myapp"),
		"myapp/.templates/default/config.yaml":    appClusterYAML,
		"myapp/.templates/default/instances.yaml": "master:\nstorage:\n",
		"plain/app.manifest.toml":                 manifest("plain"),
		"empty/.keep":                             "",
	})
	writeFiles(t, filepath.Join(system, "versions", "sysapp", "sysapp_1.0.0"), map[string]string{
		".templates/default/config.yaml":    appClusterYAML,
		".templates/default/instances.yaml": "master:\n",
	})
	require.NoError(t, os.MkdirAll(filepath.Join(system, "apps"), 0o750))
	require.NoError(t, os.Symlink("../versions/sysapp/sysapp_1.0.0",
		filepath.Join(system, "apps", "sysapp")))

	env := []string{"TT_SYSTEM_ROOT=" + system, "TT_USER_ROOT=" + filepath.Join(dir, "user")}

	for _, testCase := range []struct {
		name string
		args []string
		dir  string
		// stdout is the whole output, or its start when prefix is set.
		stdout string
		prefix bool
	}{
		{
			name: "application", args: []string{"demo", "cluster", "myapp", "storage"},
			dir: "myapp", stdout: "master storage\nmode=ro\n",
		},
		{
			name: "application instance override",
			args: []string{"demo", "cluster", "myapp", "master"},
			dir:  "myapp", stdout: "master storage\nmode=rw\n",
		},
		{
			name: "unknown instance", args: []string{"demo", "cluster", "myapp", "nosuch"},
			dir: "myapp", stdout: "master storage\nnot found: instance \"nosuch\" not found\n",
		},
		{
			name: "application of the system scope",
			args: []string{"demo", "cluster", "sysapp", "master"},
			dir:  "empty", stdout: "master storage\nmode=rw\n",
		},
		{
			name: "unknown application", args: []string{"demo", "cluster", "nosuch"},
			dir: "myapp", prefix: true,
			stdout: "not found: cluster configuration of application \"nosuch\": " +
				"application not found: \"nosuch\" is not installed in the system scope",
		},
		{
			name: "application without a pair", args: []string{"demo", "cluster", "plain"},
			dir: "plain", prefix: true,
			stdout: "not found: cluster configuration of application \"plain\": " +
				"no instances description",
		},
		{
			name: "no application anywhere", args: []string{"demo", "cluster", "myapp"},
			dir: "empty", prefix: true,
			stdout: "not found: cluster configuration of application \"myapp\": " +
				"application not found",
		},
		{
			name: "file", args: []string{
				"demo", "cluster", filepath.Join(dir, "myapp", ".templates", "default",
					"config.yaml"), "storage",
			},
			dir: "empty", stdout: "master storage\nmode=ro\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := runTT(t, ttRun{
				program: "demo", args: testCase.args, env: env,
				dir: filepath.Join(dir, testCase.dir),
			})

			require.Equal(t, 0, got.code, got.stderr)

			if testCase.prefix {
				assert.True(t, strings.HasPrefix(got.stdout, testCase.stdout), got.stdout)
			} else {
				assert.Equal(t, testCase.stdout, got.stdout)
			}
		})
	}
}

// TestMainClusterConfigDir checks that Services.ClusterConfig reports the
// directory relative paths in the configuration are relative to: the
// application directory, where its instances run, for an application, and
// the directory of the file for a file.
func TestMainClusterConfigDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	writeFiles(t, dir, map[string]string{
		"work/myapp/.templates/default/config.yaml":    appClusterYAML,
		"work/myapp/.templates/default/instances.yaml": "master:\nstorage:\n",
		"work/myapp/src/.keep":                         "",
		"elsewhere/cluster.yaml":                       appClusterYAML,
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
		cwd  string
		dir  string
	}{
		{
			name: "application",
			args: []string{"demo", "cluster-dir", "myapp"},
			cwd:  filepath.Join(dir, "work", "myapp", "src"),
			dir:  want(filepath.Join(dir, "work", "myapp")),
		},
		{
			name: "file",
			args: []string{"demo", "cluster-dir", filepath.Join(dir, "elsewhere", "cluster.yaml")},
			cwd:  t.TempDir(),
			dir:  want(filepath.Join(dir, "elsewhere")),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := runTT(t, ttRun{program: "demo", args: testCase.args, dir: testCase.cwd})
			require.Equal(t, 0, got.code, got.stderr)

			reported := strings.TrimSpace(got.stdout)
			require.True(t, filepath.IsAbs(reported), reported)

			resolved := want(reported)
			assert.Equal(t, testCase.dir, resolved)
		})
	}
}
