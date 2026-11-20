package running

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity/v3"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/internal/instances"
)

// validatedInstances returns the contexts of the instances names of the
// application app whose cluster config is config, as NewInstanceCtx makes
// them, for ValidateConfigs.
func validatedInstances(t *testing.T, app, config string, names ...string) []InstanceCtx {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(config), 0o644))

	mut, err := cluster.GetClusterConfig(t.Context(), path, integrity.IntegrityCtx{})
	require.NoError(t, err)

	insts := make([]InstanceCtx, 0, len(names))

	for _, name := range names {
		cfg, err := cluster.GetInstanceConfig(mut, name)
		require.NoError(t, err)

		insts = append(insts, InstanceCtx{
			AppName: app, InstName: name, Kind: instances.KindCluster,
			ClusterConfigPath: path, Configuration: &cfg, clusterConfig: mut,
		})
	}

	return insts
}

const validTopology = "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
	"          i1: {}\n          i2: {}\n"

//nolint:paralleltest // Reads the TT_* environment.
func TestValidateConfigsValid(t *testing.T) {
	insts := validatedInstances(t, "app", "app:\n  file: init.lua\n"+validTopology, "i1", "i2")

	require.NoError(t, ValidateConfigs(insts))
	// Nothing to validate for an instance the cluster config does not
	// configure.
	require.NoError(t, ValidateConfigs([]InstanceCtx{{Kind: instances.KindScript}}))
}

//nolint:paralleltest // Reads the TT_* environment.
func TestValidateConfigsRefused(t *testing.T) {
	insts := validatedInstances(t, "app",
		"memtx:\n  memroy: 100\n  memory: abc\n"+validTopology, "i1", "i2")

	err := ValidateConfigs(insts)
	require.ErrorIs(t, err, ErrInvalidConfig)

	// The config is reported once, every problem on a line of its own, an
	// unknown key as an unknown option.
	assert.Equal(t, `application "app": the cluster config `+insts[0].ClusterConfigPath+
		" is invalid:\n"+
		"  memtx.memory: value does not match pattern: Value does not match the required "+
		`pattern ^(?:\d+(?:\.\d*)?|\.\d+)\s*(?:[Bb]|[Kk][Ii][Bb]|[Mm][Ii][Bb]|[Gg][Ii][Bb]|`+
		`[Tt][Ii][Bb]|[Pp][Ii][Bb])?$`+"\n"+
		"  unknown option memtx.memroy", err.Error())
}

//nolint:paralleltest // Reads the TT_* environment.
func TestValidateConfigsOfAnInstance(t *testing.T) {
	// The key of an instance is reported by its path in the cluster config.
	insts := validatedInstances(t, "app", "groups:\n  g:\n    replicasets:\n      r:\n"+
		"        instances:\n          i1:\n            iproto: {lisen: x}\n", "i1")

	err := ValidateConfigs(insts)
	require.ErrorIs(t, err, ErrInvalidConfig)
	assert.Contains(t, err.Error(),
		"  unknown option groups.g.replicasets.r.instances.i1.iproto.lisen")
}
