package cluster_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-storage"

	"github.com/tarantool/tt/lib/cluster"
)

type mockFileCollector struct {
	path string
}

func (mock mockFileCollector) Collect() ([]cluster.Data, error) {
	return nil, errors.New("not implemented")
}

type mockStorageCollector struct {
	storage storage.Storage
	prefix  string
	key     string
}

func (mock mockStorageCollector) Collect() ([]cluster.Data, error) {
	return nil, errors.New("not implemented")
}

type mockDataCollectorFactory struct{}

func (mock mockDataCollectorFactory) NewFile(path string) (cluster.DataCollector, error) {
	return mockFileCollector{
		path: path,
	}, nil
}

func (mock mockDataCollectorFactory) NewRemoteStorage(
	storage storage.Storage,
	prefix, key string,
	_ time.Duration,
	storageType string,
) (cluster.DataCollector, error) {
	return mockStorageCollector{
		storage: storage,
		prefix:  prefix,
		key:     key,
	}, nil
}

func TestCollectorFactory(t *testing.T) {
	factory := cluster.NewCollectorFactory(mockDataCollectorFactory{})

	noErr := func(collector cluster.Collector, err error) cluster.Collector {
		require.NoError(t, err)
		return collector
	}

	cases := []struct {
		Name      string
		Collector cluster.Collector
		Expected  cluster.Collector
	}{
		{
			Name:      "file",
			Collector: noErr(factory.NewFile("foo")),
			Expected: cluster.NewYamlCollectorDecorator(mockFileCollector{
				path: "foo",
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Collector)
		})
	}
}
