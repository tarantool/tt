package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cluster"
)

// TestValidateInstance pins that the folded config of an instance keeps the
// keys only a replicaset may set, and still refuses the keys Tarantool does
// not know and the values of a wrong type.
func TestValidateInstance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data string
		err  string
	}{
		{name: "empty", data: ""},
		{name: "instance keys", data: "iproto:\n  listen:\n    - uri: 127.0.0.1:3301\n"},
		{
			name: "replicaset keys",
			data: "replication:\n  failover: manual\nleader: i1\nbootstrap_leader: i1\n",
		},
		{
			name: "leader of a wrong type",
			data: "leader: 123\n",
			err:  "leader [type] invalid type",
		},
		{
			name: "unknown key",
			data: "leader: i1\nfoo: bar\n",
			err:  "foo [schema] No values are allowed because the schema is set to 'false'",
		},
		{
			name: "instances of the replicaset",
			data: "instances:\n  i1: {}\n",
			err:  "instances [schema] No values are allowed because the schema is set to 'false'",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := cluster.BuildGoConfigFromBytes(t.Context(), []byte(test.data))
			require.NoError(t, err)

			err = cluster.ValidateInstance(cfg)
			if test.err == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.err)
			}

			// The top of the cluster config does not take the replicaset keys.
			err = cluster.Validate(cfg)
			if test.name == "replicaset keys" {
				require.ErrorContains(t, err,
					"leader [schema] No values are allowed because the schema is set to 'false'")
			}
		})
	}
}
