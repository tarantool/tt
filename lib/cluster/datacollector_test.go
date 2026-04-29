package cluster_test

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/lib/cluster"
)

func TestDataCollectorFactory_NewFile_not_exist(t *testing.T) {
	cases := []struct {
		Name    string
		Factory cluster.DataCollectorFactory
	}{
		{
			Name:    "base",
			Factory: cluster.NewDataCollectorFactory(),
		},
		{
			Name: "integrity",
			Factory: cluster.NewIntegrityDataCollectorFactory(nil,
				func(path string) (io.ReadCloser, error) {
					return os.Open(path)
				}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			collector, err := tc.Factory.NewFile("some/invalid/path")
			require.NoError(t, err)

			_, err = collector.Collect()
			assert.Error(t, err)
		})
	}
}

func TestDataCollectorFactory_NewFile_valid(t *testing.T) {
	expected := []cluster.Data{{
		Source: testYamlPath,
		Value: []byte(`config:
  version: 3.0.0
  hooks:
    post_cfg: /foo
    on_state_change: /bar
etcd:
  endpoints:
    - http://foo:4001
    - bar
  username: etcd
  password: not_a_secret
`),
	}}

	cases := []struct {
		Name    string
		Factory cluster.DataCollectorFactory
	}{
		{
			Name:    "base",
			Factory: cluster.NewDataCollectorFactory(),
		},
		{
			Name: "integrity",
			Factory: cluster.NewIntegrityDataCollectorFactory(nil,
				func(path string) (io.ReadCloser, error) {
					return os.Open(path)
				}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			collector, err := tc.Factory.NewFile(testYamlPath)
			require.NoError(t, err)

			data, err := collector.Collect()
			require.NoError(t, err)
			require.Equal(t, expected, data)
		})
	}
}
