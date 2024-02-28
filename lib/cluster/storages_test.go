package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/lib/cluster"
)

func TestStorage_GetStorageKey(t *testing.T) {
	cases := []struct {
		prefix string
		source string
		key    string
		err    string
	}{
		{"/foo", "/foo/config/all", "all", ""},
		{"/foo/", "/foo/config/all", "all", ""},
		{"/foo///", "/foo/config/all", "all", ""},
		{"/foo", "/bar/config/all", "", "source must begin with: /foo/config/"},
	}
	for _, cs := range cases {
		t.Run(cs.source, func(t *testing.T) {
			key, err := cluster.GetStorageKey(cs.prefix, cs.source)
			if cs.err != "" {
				require.EqualError(t, err, cs.err)
			} else {
				require.NoError(t, err)
				require.Equal(t, cs.key, key)
			}
		})
	}
}
