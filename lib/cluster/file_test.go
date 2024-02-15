package cluster_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/lib/cluster"
)

const (
	invalidYamlPath = "testdata/invalid.yaml"
	testYamlPath    = "testdata/test.yaml"
)

func TestNewFileCollector(t *testing.T) {
	var collector cluster.DataCollector

	collector = cluster.NewFileCollector(testYamlPath)

	assert.NotNil(t, collector)
}

func TestNewFileCollector_not_exist(t *testing.T) {
	collector := cluster.NewFileCollector("some/invalid/path")

	_, err := collector.Collect()
	assert.Error(t, err)
}

func TestFileCollector_valid(t *testing.T) {
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

	collector := cluster.NewFileCollector(testYamlPath)

	data, err := collector.Collect()
	require.NoError(t, err)
	require.Equal(t, expected, data)
}

func TestNewFileDataPublisher(t *testing.T) {
	var publisher cluster.DataPublisher

	publisher = cluster.NewFileDataPublisher("")
	assert.NotNil(t, publisher)
}

func TestFileDataPublisher_Publish_empty_path(t *testing.T) {
	err := cluster.NewFileDataPublisher("").Publish(0, []byte{})

	assert.EqualError(t, err, "file path is empty")
}

func TestFileDataPublisher_Publish_empty_data(t *testing.T) {
	err := cluster.NewFileDataPublisher("foo").Publish(0, nil)

	assert.EqualError(t, err,
		"failed to publish data into \"foo\": data does not exist")
}

func TestFileDataPublisher_Publish_revision(t *testing.T) {
	err := cluster.NewFileDataPublisher("foo").Publish(1, []byte{})

	assert.EqualError(t, err,
		"failed to publish data into file: target revision 1 is not supported")
}

func TestFileDataPublisher_Publish_error(t *testing.T) {
	err := cluster.NewFileDataPublisher("/some/invalid/path").Publish(0, []byte{})

	assert.Error(t, err)
}

func TestFileDataPublisher_Publish_data(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")

	data := []byte("foo")
	err := cluster.NewFileDataPublisher(path).Publish(0, data)
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
	err = cluster.NewFileDataPublisher(path).Publish(0, data)
	require.NoError(t, err)

	read, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, read)

	fi, err = os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, originalMode, fi.Mode())
}
