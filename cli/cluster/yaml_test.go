package cluster_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/integrity"
)

func TestNewYamlCollector(t *testing.T) {
	var collector cluster.Collector
	collector = cluster.NewYamlCollector(nil)
	assert.NotNil(t, collector)
}

func TestYamlCollector_valid(t *testing.T) {
	data := []byte(`config:
  version: 3.0.0
  hooks:
    post_cfg: /foo
    on_state_change: /bar
etcd:
  endpoints:
    - http://foo:4001
    - bar
  username: etcd
  password: not_a_secret`)
	paths := []struct {
		path  []string
		value any
	}{
		{[]string{"config", "version"}, "3.0.0"},
		{[]string{"config", "hooks", "post_cfg"}, "/foo"},
		{[]string{"config", "hooks", "on_state_change"}, "/bar"},
		{[]string{"etcd", "endpoints"}, []any{"http://foo:4001", "bar"}},
		{[]string{"etcd", "username"}, "etcd"},
		{[]string{"etcd", "password"}, "not_a_secret"},
	}
	collector := cluster.NewYamlCollector(data)

	config, err := collector.Collect()
	require.NoError(t, err)
	require.NotNil(t, config)

	for _, p := range paths {
		t.Run(fmt.Sprintf("%v", p.path), func(t *testing.T) {
			value, err := config.Get(p.path)
			assert.NoError(t, err)
			assert.Equal(t, p.value, value)
		})
	}
}

func TestYamlCollector_invalid(t *testing.T) {
	data := []byte(`config: asd
- foo
`)
	collector := cluster.NewYamlCollector(data)

	_, err := collector.Collect()
	assert.Error(t, err)
}

func TestYamlCollector_empty(t *testing.T) {
	data := [][]byte{
		nil,
		[]byte(""),
	}

	for _, d := range data {
		t.Run(fmt.Sprintf("%v", data), func(t *testing.T) {
			collector := cluster.NewYamlCollector(d)
			config, err := collector.Collect()

			assert.NoError(t, err)
			assert.NotNil(t, config)
		})
	}
}

func TestYamlCollector_unique(t *testing.T) {
	collector := cluster.NewYamlCollector([]byte("config: asd"))
	require.NotNil(t, collector)

	config1, err := collector.Collect()
	require.NoError(t, err)
	config2, err := collector.Collect()
	require.NoError(t, err)

	path := []string{"foo"}
	require.Nil(t, config1.Set(path, "bar"))
	_, err = config2.Get(path)
	assert.Error(t, err)
}

type mockDataCollector struct {
	data []integrity.Data
	err  error
}

func (mock mockDataCollector) Collect() ([]integrity.Data, error) {
	return mock.data, mock.err
}

func newYamlDecoratorMocked(data []byte, err error) cluster.YamlCollectorDecorator {
	return cluster.NewYamlCollectorDecorator(mockDataCollector{
		data: []integrity.Data{{
			Value: data,
		}},
		err: err,
	})
}

func TestNewYamlCollectorDecorator(t *testing.T) {
	var collector cluster.Collector
	collector = cluster.NewYamlCollectorDecorator(nil)
	assert.NotNil(t, collector)
}

func TestYamlCollectorDecorator_valid(t *testing.T) {
	data := []byte(`config:
  version: 3.0.0
  hooks:
    post_cfg: /foo
    on_state_change: /bar
etcd:
  endpoints:
    - http://foo:4001
    - bar
  username: etcd
  password: not_a_secret`)
	paths := []struct {
		path  []string
		value any
	}{
		{[]string{"config", "version"}, "3.0.0"},
		{[]string{"config", "hooks", "post_cfg"}, "/foo"},
		{[]string{"config", "hooks", "on_state_change"}, "/bar"},
		{[]string{"etcd", "endpoints"}, []any{"http://foo:4001", "bar"}},
		{[]string{"etcd", "username"}, "etcd"},
		{[]string{"etcd", "password"}, "not_a_secret"},
	}
	collector := newYamlDecoratorMocked(data, nil)

	config, err := collector.Collect()
	require.NoError(t, err)
	require.NotNil(t, config)

	for _, p := range paths {
		t.Run(fmt.Sprintf("%v", p.path), func(t *testing.T) {
			value, err := config.Get(p.path)
			assert.NoError(t, err)
			assert.Equal(t, p.value, value)
		})
	}
}

func TestYamlCollectorDecorator_merge(t *testing.T) {
	kvs := []integrity.Data{
		{
			Source: "k",
			Value:  []byte("f: a\nb: a\n"),
		},
		{
			Source: "k",
			Value:  []byte("f: b\nb: b\nc: b\n"),
		},
	}
	expected := "b: a\nc: b\nf: a\n"

	collector := cluster.NewYamlCollectorDecorator(mockDataCollector{
		data: kvs,
		err:  nil,
	})

	config, err := collector.Collect()
	require.NoError(t, err)
	require.Equal(t, expected, config.String())
}

func TestYamlCollectorDecorator_invalid(t *testing.T) {
	data := []byte(`config: asd
- foo
`)
	collector := cluster.NewYamlCollectorDecorator(mockDataCollector{
		data: []integrity.Data{{
			Source: "test",
			Value:  data,
		}},
		err: nil,
	})

	_, err := collector.Collect()
	assert.ErrorContains(t, err, "failed to decode config from \"test\"")
}

func TestYamlCollectorDecorator_empty(t *testing.T) {
	data := [][]byte{
		nil,
		[]byte(""),
	}

	for _, d := range data {
		t.Run(fmt.Sprintf("%v", data), func(t *testing.T) {
			collector := newYamlDecoratorMocked(d, nil)
			config, err := collector.Collect()

			assert.NoError(t, err)
			assert.NotNil(t, config)
		})
	}
}

func TestYamlCollectorDecorator_unique(t *testing.T) {
	collector := newYamlDecoratorMocked([]byte("config: asd"), nil)
	require.NotNil(t, collector)

	config1, err := collector.Collect()
	require.NoError(t, err)
	config2, err := collector.Collect()
	require.NoError(t, err)

	path := []string{"foo"}
	require.Nil(t, config1.Set(path, "bar"))
	_, err = config2.Get(path)
	assert.Error(t, err)
}

type dataPublishFunc func(data []byte) error

func (f dataPublishFunc) Publish(data []byte) error {
	return f(data)
}

func TestNewYamlConfigPublisher(t *testing.T) {
	var publisher cluster.ConfigPublisher
	publisher = cluster.NewYamlConfigPublisher(nil)
	assert.NotNil(t, publisher)
}

func TestYamlConfigPublisher_Publish_nil_publisher(t *testing.T) {
	publisher := cluster.NewYamlConfigPublisher(nil)
	config := cluster.NewConfig()

	assert.Panics(t, func() {
		publisher.Publish(config)
	})
}

func TestYamlConfigPublisher_Publish_nil_config(t *testing.T) {
	publisher := cluster.NewYamlConfigPublisher(nil)
	err := publisher.Publish(nil)

	assert.EqualError(t, err, "config does not exist")
}

func TestYamlConfigPublisher_Publish_publish_data(t *testing.T) {
	var input []byte
	publisher := cluster.NewYamlConfigPublisher(dataPublishFunc(
		func(data []byte) error {
			input = data
			return nil
		}))
	config := cluster.NewConfig()
	config.Set([]string{"foo"}, "bar")
	config.Set([]string{"zoo", "foo"}, []any{1, 2, 3})

	err := publisher.Publish(config)
	require.NoError(t, err)
	assert.Equal(t, `foo: bar
zoo:
  foo:
    - 1
    - 2
    - 3
`, string(input))
}

func TestYamlConfigPublisher_Publish_error(t *testing.T) {
	err := fmt.Errorf("any")
	publisher := cluster.NewYamlConfigPublisher(dataPublishFunc(
		func([]byte) error {
			return err
		}))
	config := cluster.NewConfig()

	errPublish := publisher.Publish(config)
	assert.ErrorIs(t, errPublish, err)
}
