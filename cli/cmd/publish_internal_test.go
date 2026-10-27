package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// topologyOf returns a cluster config declaring the instances names, one
// replicaset of one group.
func topologyOf(names ...string) string {
	var config strings.Builder

	config.WriteString("groups:\n  g:\n    replicasets:\n      r:\n        instances:\n")

	for _, name := range names {
		fmt.Fprintf(&config, "          %s: {}\n", name)
	}

	return config.String()
}

// publishProject lays out the project myapp, the -C directory and the
// working directory, whose default template is description and config, and
// returns the path of its config.
func publishProject(t *testing.T, description, config string) string {
	t.Helper()

	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	project := filepath.Join(t.TempDir(), "myapp")
	writeFiles(t, project, map[string]string{
		".templates/default/instances.yaml": description,
		".templates/default/config.yaml":    config,
	})

	withProjectDir(t, project)
	t.Chdir(project)

	// The constructor binds --scope and --template anew, to their defaults.
	findCommand(t, NewClusterCmd(), []string{"publish"})

	return filepath.Join(project, ".templates", "default", "config.yaml")
}

// publishSource writes config into a file to publish and returns its path.
func publishSource(t *testing.T, config string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "source.yaml")
	require.NoError(t, os.WriteFile(path, []byte(config), 0o644))

	return path
}

// TestClusterPublishChecksTopology pins that tt cluster publish into the
// pair of an application checks the config it publishes against the
// description: a config that leaves out a Tarantool instance of the
// description, or declares an instance of a name a script or an external
// process takes, is refused and the pair is left as it was.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishChecksTopology(t *testing.T) {
	const description = "storage-1:\nlegacy-1:\n  script: init.lua\n" +
		"worker-1:\n  binary: ./bin/worker\n"

	cases := []struct {
		name   string
		source string
		err    error
		msg    []string
	}{
		{
			name:   "an instance of the description left out",
			source: topologyOf("storage-2"),
			err:    app.ErrTopologyMismatch,
			msg:    []string{`"storage-1" from `, "not declared in the config to publish, /"},
		},
		{
			name:   "an instance named as a script",
			source: topologyOf("storage-1", "legacy-1", "worker-1"),
			err:    app.ErrTopologyConflict,
			msg:    []string{`"legacy-1", "worker-1" of `, "with script or binary"},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := publishProject(t, description, topologyOf("storage-1", "storage-2"))
			source := publishSource(t, test.source)

			err := internalClusterPublishModule(&cmdcontext.CmdCtx{}, []string{"myapp", source})
			require.ErrorIs(t, err, test.err)
			assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))

			for _, msg := range test.msg {
				require.ErrorContains(t, err, msg)
			}

			data, err := os.ReadFile(config)
			require.NoError(t, err)
			assert.Equal(t, topologyOf("storage-1", "storage-2"), string(data),
				"the config is not written")
		})
	}

	t.Run("a config that keeps the instances", func(t *testing.T) {
		config := publishProject(t, description, topologyOf("storage-1", "storage-2"))
		source := publishSource(t, topologyOf("storage-1", "storage-3"))

		require.NoError(t, internalClusterPublishModule(&cmdcontext.CmdCtx{},
			[]string{"myapp", source}))

		data, err := os.ReadFile(config)
		require.NoError(t, err)
		assert.Equal(t, topologyOf("storage-1", "storage-3"), string(data))
	})

	t.Run("the config of an instance", func(t *testing.T) {
		config := publishProject(t, description, topologyOf("storage-1", "storage-2"))
		source := publishSource(t, "iproto:\n  listen:\n    - uri: 127.0.0.1:3399\n")

		require.NoError(t, internalClusterPublishModule(&cmdcontext.CmdCtx{},
			[]string{"myapp:storage-1", source}))

		data, err := os.ReadFile(config)
		require.NoError(t, err)
		assert.Contains(t, string(data), "127.0.0.1:3399")
		assert.Contains(t, string(data), "storage-2")
	})
}

// TestClusterPublishMendsPair pins that tt cluster publish reads the pair it
// publishes into without checking its description against the topology: a
// pair whose config lost an instance of the description, which every other
// command refuses, is mended by publishing a config with it.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishMendsPair(t *testing.T) {
	config := publishProject(t, "storage-1:\nstorage-9:\n", topologyOf("storage-1"))

	_, _, err := parseAppStr(&cmdcontext.CmdCtx{}, "myapp")
	require.ErrorIs(t, err, app.ErrTopologyMismatch, "tt cluster show checks the pair")

	source := publishSource(t, topologyOf("storage-9"))

	err = internalClusterPublishModule(&cmdcontext.CmdCtx{}, []string{"myapp", source})
	require.ErrorIs(t, err, app.ErrTopologyMismatch, "storage-1 is dropped now")

	source = publishSource(t, topologyOf("storage-1", "storage-9"))

	require.NoError(t, internalClusterPublishModule(&cmdcontext.CmdCtx{},
		[]string{"myapp", source}))

	data, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, topologyOf("storage-1", "storage-9"), string(data))

	_, _, err = parseAppStr(&cmdcontext.CmdCtx{}, "myapp")
	require.NoError(t, err)
}

// TestClusterPublishWildcard pins the choice made where the decision is
// silent: the instances "*" stands for now are instances of the
// description, and a config that drops one of them is not published into
// the pair, which would leave it running without its config.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishWildcard(t *testing.T) {
	config := publishProject(t, "\"*\":\n", topologyOf("s-1", "s-2"))

	source := publishSource(t, topologyOf("s-1"))

	err := internalClusterPublishModule(&cmdcontext.CmdCtx{}, []string{"myapp", source})
	require.ErrorIs(t, err, app.ErrTopologyMismatch)
	require.ErrorContains(t, err, `"s-2" from `)
	require.ErrorContains(t, err, `"*" stands for the instances the pair runs now`)

	source = publishSource(t, topologyOf("s-1", "s-2", "s-3"))

	require.NoError(t, internalClusterPublishModule(&cmdcontext.CmdCtx{},
		[]string{"myapp", source}))

	data, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, topologyOf("s-1", "s-2", "s-3"), string(data))
}

// TestClusterPublishIncompletePair pins that a pair that has lost the config
// its description needs is refused as incomplete, as every other command
// refuses it, though tt cluster publish reads the config by the best effort.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishIncompletePair(t *testing.T) {
	config := publishProject(t, "storage-1:\n", topologyOf("storage-1"))
	require.NoError(t, os.Remove(config))

	source := publishSource(t, topologyOf("storage-1"))

	err := internalClusterPublishModule(&cmdcontext.CmdCtx{}, []string{"myapp", source})
	require.ErrorIs(t, err, app.ErrIncompletePair)
	require.ErrorContains(t, err, `to configure its Tarantool instances "storage-1"`)
	assert.NoFileExists(t, config, "no config is made up for the pair")
}
