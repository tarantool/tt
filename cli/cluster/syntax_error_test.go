package cluster_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-config/v2/collectors"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cluster"
)

// TestSyntaxErrorOfConfigFile pins that a cluster config file that is no
// YAML is told by its position, <path>:<line>[:<column>]: <problem>, rather
// than by the key go-config names the file by.
func TestSyntaxErrorOfConfigFile(t *testing.T) {
	cases := map[string]struct {
		data string
		want string
	}{
		"a mapping value out of place": {
			data: "groups: {}\na: b: c\n",
			want: ":2:4: mapping values are not allowed in this context",
		},
		"a quote not closed": {
			data: "groups: {}\nx: 'open\n",
			want: ":2:4: found unexpected end of stream",
		},
		"a tab": {
			data: "groups: {}\n\tfoo: 1\n",
			want: ":2:1: found character that cannot start any token",
		},
		"a flow mapping not closed": {
			data: "memtx: {memory: 1, x\n",
			want: ":1:20: did not find expected ',' or '}'",
		},
		// yaml.v3 tells the line before the problem here.
		"a flow mapping not closed after a line": {
			data: "groups: {}\nmemtx: {memory: 1, x\n",
			want: ":2:20: did not find expected ',' or '}'",
		},
	}

	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(broken.data), 0o644))

			_, err := cluster.GetClusterConfig(t.Context(), path, integrity.IntegrityCtx{})
			require.Error(t, err)

			var syntax *cluster.SyntaxError

			require.ErrorAs(t, err, &syntax)
			assert.Equal(t, "unable to load config: "+path+broken.want, err.Error())
			assert.NotContains(t, err.Error(), `key "file"`)
			require.ErrorIs(t, err, collectors.ErrUnmarshall)
		})
	}
}
