package app_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/internal/instances"
)

// TestWildcard pins what "*" stands for when the cluster config is read: the
// instances of the topology that no record names, in the order of their
// names, where "*" is written.
func TestWildcard(t *testing.T) {
	cases := []struct {
		name   string
		desc   string
		config string
		want   []string // The names of the instances.
		err    error
		msg    []string // Parts of the message.
	}{
		{
			name:   "alone",
			desc:   "\"*\":\n",
			config: topology("storage-2", "router-1", "storage-1"),
			want:   []string{"router-1", "storage-1", "storage-2"},
		},
		{
			name: "in its place",
			desc: "router-1:\n\"*\":\nworker-1:\n  binary: ./bin/worker\n" +
				"legacy-1:\n  script: init.lua\n",
			config: topology("b-2", "router-1", "b-1"),
			want:   []string{"router-1", "b-1", "b-2", "worker-1", "legacy-1"},
		},
		{
			name:   "a record of another kind with a name of the topology",
			desc:   "\"*\":\nstorage-1:\n  binary: ./bin/fake\n",
			config: topology("storage-1", "storage-2"),
			want:   []string{"storage-2", "storage-1"},
		},
		{
			name:   "every instance named",
			desc:   "storage-1:\n\"*\":\nstorage-2:\n",
			config: topology("storage-1", "storage-2"),
			want:   []string{"storage-1", "storage-2"},
		},
		{
			name:   "an empty topology",
			desc:   "\"*\":\nworker-1:\n  binary: ./bin/worker\n",
			config: topology(),
			want:   []string{"worker-1"},
		},
		{
			name:   "the records are still checked against the topology",
			desc:   "\"*\":\nstorage-9:\n",
			config: topology("storage-1"),
			err:    app.ErrTopologyMismatch,
			msg:    []string{`"storage-9"`, "not declared in"},
		},
		{
			name:   "workdir in the record",
			desc:   "\"*\":\n  workdir: var\n",
			config: topology("storage-1"),
			err:    app.ErrUnsupportedField,
			msg:    []string{`record "*": unsupported field workdir`},
		},
		{
			name:   "logrotate_signal in the record",
			desc:   "\"*\": {logrotate_signal: HUP}\n",
			config: topology("storage-1"),
			err:    app.ErrUnsupportedField,
			msg:    []string{`record "*"`, "logrotate_signal"},
		},
		{
			name:   "binary in the record",
			desc:   "\"*\":\n  binary: ./bin/worker\n",
			config: topology("storage-1"),
			err:    instances.ErrInvalid,
			msg:    []string{`record "*": field "binary" is not allowed`},
		},
		{
			name:   "unquoted",
			desc:   "router-1:\n*:\n",
			config: topology("router-1"),
			err:    instances.ErrInvalid,
			msg:    []string{"line 2:", `write "*": in quotes`},
		},
		{
			name:   "a config that does not parse",
			desc:   "\"*\":\n",
			config: "groups: [\n",
			msg:    []string{"config.yaml"},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			roots(t)

			tree := t.TempDir()
			writeTree(t, tree, map[string]string{
				".templates/default/instances.yaml": test.desc,
				".templates/default/config.yaml":    test.config,
			})

			got, err := load(t, "", app.Options{Start: tree})
			if test.want != nil {
				require.NoError(t, err)
				assert.Equal(t, test.want, names(got.Instances))
				assert.True(t, got.HasClusterInstances())

				return
			}

			require.Error(t, err)
			assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))

			if test.err != nil {
				require.ErrorIs(t, err, test.err)
			}

			for _, msg := range test.msg {
				require.ErrorContains(t, err, msg)
			}
		})
	}
}

// TestWildcardFields checks the instances of "*" get its fields, and a record
// naming one of them replaces "*" for it whole.
func TestWildcardFields(t *testing.T) {
	roots(t)

	tree := t.TempDir()
	writeTree(t, tree, map[string]string{
		".templates/default/instances.yaml": "\"*\":\n  restart: always\n  stop_signal: INT\n" +
			"  stop_timeout: 5s\n  env:\n    A: b\nstorage-2:\n  restart: on-failure\n",
		".templates/default/config.yaml": topology("storage-1", "storage-2", "storage-3"),
	})

	got, err := load(t, "", app.Options{Start: tree})
	require.NoError(t, err)
	require.Equal(t, []string{"storage-1", "storage-3", "storage-2"}, names(got.Instances))

	for _, inst := range got.Instances[:2] {
		assert.Equal(t, instances.Instance{
			Name:        inst.Name,
			Env:         map[string]string{"A": "b"},
			StopSignal:  syscall.SIGINT,
			StopTimeout: 5 * time.Second,
			Restart:     instances.RestartAlways,
		}, inst)
	}

	assert.Equal(t, instances.Instance{
		Name:    "storage-2",
		Restart: instances.RestartOnFailure,
	}, got.Instances[2], "nothing of \"*\" goes into a record")

	got.Instances[0].Env["A"] = "c"
	assert.Equal(t, "b", got.Instances[1].Env["A"], "the instances share no env")
}

// TestWildcardSelection checks the targets select the instances of "*" as
// they select the others, and "*" standing for none selects nothing.
func TestWildcardSelection(t *testing.T) {
	roots(t)

	tree := t.TempDir()
	writeTree(t, tree, map[string]string{
		"app.manifest.toml":                 manifestOf("myapp"),
		".templates/default/instances.yaml": "worker-1:\n  binary: ./bin/worker\n\"*\":\n",
		".templates/default/config.yaml":    topology("storage-1", "router-1"),
		".templates/empty/instances.yaml":   "\"*\":\n",
		".templates/empty/config.yaml":      topology(),
		".templates/script/instances.yaml":  "\"*\":\nlegacy-1:\n  script: init.lua\n",
		".templates/script/config.yaml":     topology(),
	})

	ctx := context.Background()

	selections, err := app.Resolve(ctx, []app.Target{{App: "myapp", Instance: "router-1"}},
		app.Options{Start: tree})
	require.NoError(t, err)
	require.Len(t, selections, 1)
	assert.Equal(t, []string{"router-1"}, names(selections[0].Instances))

	selections, err = app.Resolve(ctx, nil, app.Options{Start: tree})
	require.NoError(t, err)
	assert.Equal(t, []string{"worker-1", "router-1", "storage-1"},
		names(selections[0].Instances))

	_, err = app.Resolve(ctx, nil, app.Options{Start: tree, Template: "empty"})
	requireFailure(t, err, app.ErrNoInstances)
	require.ErrorContains(t, err, `describes no instances, and "*" stands for those of `+
		"the topology of "+filepath.Join(tree, ".templates", "empty", "config.yaml")+
		", which declares none")

	// "*" is the topology of the cluster config, though it has no instance:
	// the application is no application of scripts alone.
	got, err := load(t, "", app.Options{Start: tree, Template: "script"})
	require.NoError(t, err)
	assert.Equal(t, []string{"legacy-1"}, names(got.Instances))
	assert.True(t, got.HasClusterInstances())
}

// TestWildcardNeedsConfig checks "*" needs the cluster config of the pair,
// as a Tarantool instance it configures does.
func TestWildcardNeedsConfig(t *testing.T) {
	roots(t)

	tree := t.TempDir()
	writeTree(t, tree, map[string]string{
		".templates/default/instances.yaml": "worker-1:\n  binary: ./bin/worker\n\"*\":\n" +
			"router-1:\n",
	})

	_, err := load(t, "", app.Options{Start: tree})
	requireFailure(t, err, app.ErrIncompletePair)
	require.ErrorContains(t, err, "has instances.yaml but no config.yaml next to it to "+
		`configure its Tarantool instances "*", "router-1"`)
}

// runDirs creates the run directories of the instances names of the
// application appName under root, as their starts leave them.
func runDirs(t *testing.T, root, appName string, names ...string) {
	t.Helper()

	for _, name := range names {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "var", "run", appName, name), 0o755))
	}
}

// TestWildcardBestEffort pins what "*" stands for to a command that reads
// the config as far as it can (Options.BestEffortConfig): the topology when
// the config can be read, and the instances that have run in the scope,
// those with a run directory, when it cannot.
func TestWildcardBestEffort(t *testing.T) {
	const desc = "worker-1:\n  binary: ./bin/worker\n\"*\":\n  restart: always\n" +
		"storage-2:\n"

	unread := []struct {
		name   string
		config string // The file is left out when it is empty.
		err    error  // What ConfigError wraps, when it is known.
	}{
		{name: "a config that does not parse", config: "groups: [\n"},
		{name: "a config storage out of reach", config: "unreachable"},
		{name: "no config", err: app.ErrIncompletePair},
	}

	for _, test := range unread {
		t.Run(test.name, func(t *testing.T) {
			roots(t)

			config := test.config
			if config == "unreachable" {
				config = unreachableConfig(t)
			}

			files := map[string]string{
				"app.manifest.toml":                 manifestOf("myapp"),
				".templates/default/instances.yaml": desc,
			}
			if config != "" {
				files[".templates/default/config.yaml"] = config
			}

			tree := t.TempDir()
			writeTree(t, tree, files)

			root := filepath.Join(tree, ".tarantool")
			runDirs(t, root, "myapp", "storage-3", "storage-2", "worker-1", "storage-1")
			// Neither a file nor the directory of another application is an
			// instance that has run.
			writeTree(t, root, map[string]string{
				"var/run/myapp/notes.txt":          "",
				"var/run/other/storage-9/tt.pid":   "",
				"var/lib/myapp/storage-8/00.snap":  "",
				"var/log/myapp/storage-7/tt.log":   "",
				".templates/default/instances.yml": "",
			})

			_, err := load(t, "", app.Options{Start: tree})
			require.Error(t, err, "the config is required without BestEffortConfig")

			got, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
			require.NoError(t, err)
			assert.Nil(t, got.Config)
			require.Error(t, got.ConfigError)

			if test.err != nil {
				require.ErrorIs(t, got.ConfigError, test.err)
			}

			assert.Equal(t, []string{"worker-1", "storage-1", "storage-3", "storage-2"},
				names(got.Instances))
			assert.Equal(t, instances.RestartAlways, got.Instances[1].Restart,
				"the instances that have run get the fields of \"*\"")
			assert.Equal(t, instances.RestartNo, got.Instances[3].Restart)
			assert.True(t, got.HasClusterInstances())
		})
	}

	t.Run("none has run", func(t *testing.T) {
		roots(t)

		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			"app.manifest.toml":                 manifestOf("myapp"),
			".templates/default/instances.yaml": "\"*\":\n",
			".templates/default/config.yaml":    "groups: [\n",
		})

		got, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
		require.NoError(t, err)
		assert.Empty(t, got.Instances)

		_, err = app.Resolve(context.Background(), nil,
			app.Options{Start: tree, BestEffortConfig: true})
		requireFailure(t, err, app.ErrNoInstances)
		require.ErrorContains(t, err, `"*" stands for those that have run in `+
			filepath.Join(tree, ".tarantool", "var", "run", "myapp")+
			", as the cluster config cannot be read, and none has")
	})

	t.Run("a config that can be read", func(t *testing.T) {
		roots(t)

		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": desc + "storage-9:\n",
			".templates/default/config.yaml":    topology("storage-1", "storage-2"),
		})
		runDirs(t, filepath.Join(tree, ".tarantool"), filepath.Base(tree), "storage-3")

		// storage-9, which the topology lacks, is not checked; and "*"
		// stands for the topology, not for what has run.
		got, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
		require.NoError(t, err)
		assert.NotNil(t, got.Config)
		require.NoError(t, got.ConfigError)
		assert.Equal(t, []string{"worker-1", "storage-1", "storage-2", "storage-9"},
			names(got.Instances))
	})

	t.Run("a config without a description", func(t *testing.T) {
		_, system := roots(t)

		install(t, system, "myapp", "1.0.0", map[string]string{
			".templates/default/config.yaml": "groups: [\n",
		})
		runDirs(t, system, "myapp", "storage-2", "storage-1")

		got, err := load(t, "myapp", app.Options{Start: t.TempDir(), BestEffortConfig: true})
		require.NoError(t, err)
		require.Error(t, got.ConfigError)
		assert.Equal(t, []string{"storage-1", "storage-2"}, names(got.Instances))
	})

	t.Run("a run directory that cannot be read", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads any directory")
		}

		roots(t)

		tree := t.TempDir()
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": "\"*\":\n",
		})

		runDir := filepath.Join(tree, ".tarantool", "var", "run", filepath.Base(tree))
		require.NoError(t, os.MkdirAll(runDir, 0o755))
		require.NoError(t, os.Chmod(runDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(runDir, 0o755) })

		_, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
		require.ErrorIs(t, err, os.ErrPermission)
		require.ErrorContains(t, err, "cannot look for the instances that have run")

		// Without "*" the directory is not looked into.
		writeTree(t, tree, map[string]string{".templates/default/instances.yaml": "storage-1:\n"})

		got, err := load(t, "", app.Options{Start: tree, BestEffortConfig: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"storage-1"}, names(got.Instances))
	})
}
