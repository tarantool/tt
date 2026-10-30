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

// TestClusterPublishWildcard pins that tt cluster publish leaves the
// instances "*" stands for unchecked: a config that drops one of them is
// published, as one that adds one is, while a config that drops an instance
// the description names next to "*" is refused.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishWildcard(t *testing.T) {
	config := publishProject(t, "\"*\":\nr-1:\n", topologyOf("r-1", "s-1", "s-2"))

	source := publishSource(t, topologyOf("s-1"))

	err := internalClusterPublishModule(&cmdcontext.CmdCtx{}, []string{"myapp", source})
	require.ErrorIs(t, err, app.ErrTopologyMismatch)
	require.ErrorContains(t, err, `"r-1" from `)
	assert.NotContains(t, err.Error(), `"s-2"`, "an instance of \"*\" may be dropped")

	data, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, topologyOf("r-1", "s-1", "s-2"), string(data), "the config is not written")

	for _, topology := range [][]string{{"r-1", "s-1"}, {"r-1", "s-1", "s-3"}, {"r-1"}} {
		source = publishSource(t, topologyOf(topology...))

		require.NoError(t, internalClusterPublishModule(&cmdcontext.CmdCtx{},
			[]string{"myapp", source}))

		data, err = os.ReadFile(config)
		require.NoError(t, err)
		assert.Equal(t, topologyOf(topology...), string(data))
	}
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

// leaderConfig returns a cluster config of one replicaset of the instances
// s-1 and s-2, with failover manual, whose lines are put at the place the
// level names: "global", "group", "replicaset" or "instance".
func leaderConfig(level, lines string) string {
	indent := map[string]string{
		"global": "", "group": "    ", "replicaset": "        ", "instance": "            ",
	}[level]

	put := func(at string) string {
		if at != level {
			return ""
		}

		var out strings.Builder

		for line := range strings.SplitSeq(strings.TrimSpace(lines), "\n") {
			out.WriteString(indent + line + "\n")
		}

		return out.String()
	}

	return "replication:\n  failover: manual\n" + put("global") +
		"groups:\n  g:\n" + put("group") +
		"    replicasets:\n      r:\n" + put("replicaset") +
		"        instances:\n          s-1:\n" + put("instance") + "          s-2: {}\n"
}

// TestClusterPublishLeaderLevel pins that tt cluster publish takes the keys
// only a replicaset may set, leader and bootstrap_leader, on the replicaset,
// as Tarantool does, and refuses them on any other level, as Tarantool
// does, as well as a key it does not know, and a leader of a wrong type.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishLeaderLevel(t *testing.T) {
	const (
		leader    = "leader: s-1"
		bootstrap = "bootstrap_leader: s-1"
		wrongKey  = "no_such_key: 1"
	)

	cases := []struct {
		name   string
		source string
		msg    string
	}{
		{name: "leader on the replicaset", source: leaderConfig("replicaset", leader)},
		{
			name:   "leader and bootstrap_leader on the replicaset",
			source: leaderConfig("replicaset", leader+"\n"+bootstrap),
		},
		{
			name:   "leader on the top",
			source: leaderConfig("global", leader),
			msg:    "leader [schema]",
		},
		{
			name:   "leader on the group",
			source: leaderConfig("group", leader),
			msg:    "leader [schema]",
		},
		{
			name:   "leader on the instance",
			source: leaderConfig("instance", leader),
			msg:    "leader [schema]",
		},
		{
			name:   "bootstrap_leader on the top",
			source: leaderConfig("global", bootstrap),
			msg:    "bootstrap_leader [schema]",
		},
		{
			name:   "bootstrap_leader on the instance",
			source: leaderConfig("instance", bootstrap),
			msg:    "bootstrap_leader [schema]",
		},
		{
			name:   "leader of a wrong type",
			source: leaderConfig("replicaset", "leader: 1"),
			msg:    "leader [type] invalid type",
		},
		{
			name:   "unknown key on the replicaset",
			source: leaderConfig("replicaset", leader+"\n"+wrongKey),
			msg:    "no_such_key [schema]",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			original := topologyOf("s-1", "s-2")
			config := publishProject(t, "s-1:\ns-2:\n", original)
			source := publishSource(t, test.source)

			err := internalClusterPublishModule(&cmdcontext.CmdCtx{}, []string{"myapp", source})

			data, readErr := os.ReadFile(config)
			require.NoError(t, readErr)

			if test.msg == "" {
				require.NoError(t, err)
				assert.Contains(t, string(data), "leader: s-1")

				return
			}

			require.ErrorContains(t, err, test.msg)
			assert.Equal(t, original, string(data), "the config is not written")
		})
	}
}
