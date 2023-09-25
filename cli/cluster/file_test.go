package cluster_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

const (
	invalidYamlPath = "testdata/invalid.yaml"
	testYamlPath    = "testdata/test.yaml"
)

func TestNewFileCollector(t *testing.T) {
	var collector cluster.Collector

	collector = cluster.NewFileCollector(testYamlPath)

	assert.NotNil(t, collector)
}

func TestNewFileCollector_not_exist(t *testing.T) {
	collector := cluster.NewFileCollector("some/invalid/path")

	_, err := collector.Collect()
	assert.Error(t, err)
}

func TestFileCollector_valid(t *testing.T) {
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
	collector := cluster.NewFileCollector(testYamlPath)

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

func TestFileCollector_invalid(t *testing.T) {
	collector := cluster.NewFileCollector(invalidYamlPath)

	_, err := collector.Collect()
	assert.Error(t, err)
}

func TestNewFileDataPublisher(t *testing.T) {
	var publisher cluster.DataPublisher

	publisher = cluster.NewFileDataPublisher("")
	assert.NotNil(t, publisher)
}

func TestFileDataPublisher_Publish_empty_path(t *testing.T) {
	err := cluster.NewFileDataPublisher("").Publish([]byte{})

	assert.EqualError(t, err, "file path is empty")
}

func TestFileDataPublisher_Publish_empty_data(t *testing.T) {
	err := cluster.NewFileDataPublisher("foo").Publish(nil)

	assert.EqualError(t, err,
		"failed to publish data into \"foo\": data does not exist")
}

func TestFileDataPublisher_Publish_error(t *testing.T) {
	err := cluster.NewFileDataPublisher("/some/invalid/path").Publish([]byte{})

	assert.Error(t, err)
}

func TestFileDataPublisher_Publish_data(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")

	data := []byte("foo")
	err := cluster.NewFileDataPublisher(path).Publish(data)
	require.NoError(t, err)

	read, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, read)

	fi, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, "-rw-r--r--", fi.Mode().String())
}

func TestFileDataPublisher_Publish_data_exist_file(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")

	err := os.WriteFile(path, []byte("bar"), 0664)
	require.NoError(t, err)
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	originalMode := fi.Mode()

	data := []byte("foo")
	err = cluster.NewFileDataPublisher(path).Publish(data)
	require.NoError(t, err)

	read, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, read)

	fi, err = os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, originalMode, fi.Mode())
}
