package replicaset_test

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/replicaset"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

//go:embed testdata/cconfig_source/*
var cconfigSourceTestDataFS embed.FS

const revision = int64(42)

func readFile(t *testing.T, path string, fs embed.FS) []byte {
	content, err := fs.ReadFile(path)
	require.NoError(t, err)
	return content
}

func readKV(t *testing.T, dir string, fs embed.FS) map[string][]byte {
	ret := map[string][]byte{}
	entries, err := fs.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		content := readFile(t, path, fs)
		key := strings.TrimRight(name, ".yml")
		ret[key] = content
	}
	return ret
}

type mockDataCollector struct {
	Ret []struct {
		Data []libcluster.Data
		Err  error
	}
	Called int
}

func (m *mockDataCollector) Collect() ([]libcluster.Data, error) {
	if m.Called >= len(m.Ret) {
		return nil, fmt.Errorf("unexpected call")
	}
	data := m.Ret[m.Called].Data
	err := m.Ret[m.Called].Err
	m.Called++
	return data, err
}

func newOnceMockDataCollector(ret []libcluster.Data, err error) *mockDataCollector {
	return &mockDataCollector{
		Ret: []struct {
			Data []libcluster.Data
			Err  error
		}{
			{Data: ret, Err: err},
		},
	}
}

type mockDataPublisher struct {
	Keys      []string
	Revisions []int64
	Data      [][]byte
	Err       []error
	Called    int
}

func (m *mockDataPublisher) Publish(key string, revision int64, data []byte) error {
	if m.Called >= len(m.Err) {
		return fmt.Errorf("unexpected call")
	}
	m.Keys = append(m.Keys, key)
	m.Revisions = append(m.Revisions, revision)
	m.Data = append(m.Data, data)
	ret := m.Err[m.Called]
	m.Called++
	return ret
}

func newOnceMockDataPublisher(err error) *mockDataPublisher {
	return &mockDataPublisher{
		Err: []error{err},
	}
}

func assertPublished(t *testing.T, p *mockDataPublisher, key string, revision int64, data []byte) {
	require.Equal(t, 1, p.Called)
	require.Equal(t, []string{key}, p.Keys)
	require.Equal(t, []int64{revision}, p.Revisions)
	require.Equal(t, [][]byte{data}, p.Data)
}

func TestCConfigSource_Promote_collect_config_error(t *testing.T) {
	err := fmt.Errorf("sharks chewed wires")
	collector := newOnceMockDataCollector(nil, err)
	source := replicaset.NewCConfigSource(collector, nil, nil)
	actual := source.Promote(replicaset.PromoteCtx{})
	require.ErrorIs(t, actual, err)
}

func TestCConfigSource_Promote_no_instance_error(t *testing.T) {
	cfg := []byte(`groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}`)
	collector := newOnceMockDataCollector([]libcluster.Data{{Value: cfg}}, nil)
	source := replicaset.NewCConfigSource(collector, nil, nil)
	actual := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
	require.ErrorContains(t, actual,
		`instance "instance-002" not found in the cluster configuration`)
}

func TestCConfigSource_Promote_unexpected_failover(t *testing.T) {
	cases := []struct {
		failover string
		errText  string
	}{
		{"election", `unsupported failover: "election", supported: "manual", "off"`},
		{"curiosity", `unknown failover, supported: "manual", "off"`},
		{"true", "unexpected failover type: bool, string expected"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.failover), func(t *testing.T) {
			cfg := []byte(fmt.Sprintf(`groups:
  group-001:
    replication:
      failover: %s
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002: {}`, tc.failover))
			collector := newOnceMockDataCollector([]libcluster.Data{
				{Value: cfg},
			}, nil)
			source := replicaset.NewCConfigSource(collector, nil, nil)
			actual := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
			require.ErrorContains(t, actual, tc.errText)
		})
	}
}

func TestCConfigSource_Promote_invalid_failover(t *testing.T) {
	cfg := []byte(`groups:
  group-001:
    replicasets:
      replicaset-001:
        replication: 42
        instances:
          instance-001: {}
          instance-002: {}`)
	collector := newOnceMockDataCollector([]libcluster.Data{
		{Value: cfg},
	}, nil)
	source := replicaset.NewCConfigSource(collector, nil, nil)
	actual := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
	require.ErrorContains(t, actual, `path ["replication"] is not a map`)
}

func TestCConfigSource_Promote_single_key(t *testing.T) {
	keyPicker := replicaset.KeyPicker(func(keys []string, _ bool) (int, error) {
		require.Equal(t, []string{"all"}, keys)
		return 0, nil
	})
	dir := filepath.Join("testdata", "cconfig_source", "promote", "single_key")
	cases := []string{
		"off_default",
		"off_explicit",
		"off_multi_master",
		"manual",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			expected := readFile(t, filepath.Join(dir, tc+"_expected.yml"),
				cconfigSourceTestDataFS)
			input := readFile(t, filepath.Join(dir, tc+"_init.yml"),
				cconfigSourceTestDataFS)
			publisher := newOnceMockDataPublisher(nil)
			collector := newOnceMockDataCollector([]libcluster.Data{
				{Source: "all", Value: input, Revision: revision},
			}, nil)
			source := replicaset.NewCConfigSource(collector, publisher, keyPicker)
			err := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
			require.NoError(t, err)
			assertPublished(t, publisher, "all", revision, expected)
		})
	}
}

func TestCConfigSource_Promote_passes_force(t *testing.T) {
	cfg := []byte(`groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}`)
	keyPicker := replicaset.KeyPicker(func(keys []string, force bool) (int, error) {
		require.True(t, force)
		return 0, nil
	})
	publisher := newOnceMockDataPublisher(nil)
	collector := newOnceMockDataCollector([]libcluster.Data{
		{Source: "all", Value: cfg},
	}, nil)
	source := replicaset.NewCConfigSource(collector, publisher, keyPicker)
	err := source.Promote(replicaset.PromoteCtx{InstName: "instance-001", Force: true})
	require.NoError(t, err)
}

func TestCConfigSource_Promote_publish_error(t *testing.T) {
	cfg := []byte(`groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002: {}`)
	err := fmt.Errorf("failed")
	publisher := newOnceMockDataPublisher(err)
	collector := newOnceMockDataCollector([]libcluster.Data{
		{Source: "all", Value: cfg},
	}, nil)
	keyPicker := replicaset.KeyPicker(func([]string, bool) (int, error) {
		return 0, nil
	})
	source := replicaset.NewCConfigSource(collector, publisher, keyPicker)
	actual := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
	require.ErrorIs(t, actual, err)
}

func TestCConfigSource_Promote_keypick_error(t *testing.T) {
	cfg := []byte(`groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002: {}`)
	publisher := newOnceMockDataPublisher(nil)
	collector := newOnceMockDataCollector([]libcluster.Data{
		{Source: "all", Value: cfg},
	}, nil)
	err := fmt.Errorf("it's too late")
	keyPicker := replicaset.KeyPicker(func([]string, bool) (int, error) {
		return 0, err
	})
	source := replicaset.NewCConfigSource(collector, publisher, keyPicker)
	actual := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
	require.ErrorIs(t, actual, err)
}

func TestCConfigSource_Promote_invalid_config(t *testing.T) {
	cfg := []byte(`no: lala
- 42`)
	collector := newOnceMockDataCollector([]libcluster.Data{
		{Source: "all", Value: cfg},
	}, nil)
	source := replicaset.NewCConfigSource(collector, nil, nil)
	err := source.Promote(replicaset.PromoteCtx{})
	require.ErrorContains(t, err, `failed to decode config from "all"`)
}

func TestCConfigSource_Promote_many_keys(t *testing.T) {
	dir := filepath.Join("testdata", "cconfig_source", "promote", "many_keys")
	cases := []struct {
		name string
		keys []string
	}{
		{"manual_priority_order", []string{"b", "a"}},
		{"off_lexi_order", []string{"a", "b"}},
		{"off_priority_order", []string{"c", "b", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testDir := filepath.Join(dir, tc.name)
			kv := readKV(t, testDir, cconfigSourceTestDataFS)
			expected, ok := kv["expected"]
			require.True(t, ok)
			delete(kv, "expected")
			var data []libcluster.Data
			for k, v := range kv {
				data = append(data, libcluster.Data{
					Source:   k,
					Value:    v,
					Revision: revision,
				})
			}
			collector := newOnceMockDataCollector(data, nil)
			publisher := newOnceMockDataPublisher(nil)
			picker := replicaset.KeyPicker(func(keys []string, _ bool) (int, error) {
				require.Equal(t, tc.keys, keys)
				return 0, nil
			})
			source := replicaset.NewCConfigSource(collector, publisher, picker)
			err := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
			require.NoError(t, err)
			assertPublished(t, publisher, tc.keys[0], revision, expected)
		})
	}
}

func TestCConfigSource_Promote_many_keys_choose_affects(t *testing.T) {
	cfg := []byte(`groups:
  group-1:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002: {}
`)
	expected := []byte(`groups:
  group-1:
    replicasets:
      replicaset-001:
        instances:
          instance-001: {}
          instance-002:
            database:
              mode: rw
`)
	collector := newOnceMockDataCollector([]libcluster.Data{
		{Source: "a", Value: cfg, Revision: 13},
		{Source: "b", Value: cfg, Revision: revision},
	}, nil)
	picker := replicaset.KeyPicker(func(keys []string, _ bool) (int, error) {
		require.Equal(t, []string{"a", "b"}, keys)
		return 1, nil
	})
	publisher := newOnceMockDataPublisher(nil)
	source := replicaset.NewCConfigSource(collector, publisher, picker)
	err := source.Promote(replicaset.PromoteCtx{InstName: "instance-002"})
	require.NoError(t, err)
	fmt.Println(string(publisher.Data[0]))
	assertPublished(t, publisher, "b", revision, expected)
}

func TestCConfigSource_Promote_mix_failovers(t *testing.T) {
	cfg1 := []byte(`groups:
  group-001:
    replicasets:
      replicaset-001:
        replication:
          failover: manual
        leader: instance-001
        instances:
          instance-001: {}
          instance-002: {}
`)
	cfg2 := []byte(`groups:
  group-001:
    replicasets:
      replicaset-002:
        replication:
          failover: off
        instances:
          instance-003: {}
          instance-004: {}
`)
	cfg3 := []byte(`groups:
  group-002:
    replicasets:
      replicaset-x:
        replication:
          failover: supervised
        instances:
          instance-005: {}
`)

	cases := []struct {
		instName string
		key      string
	}{}
	for _, tc := range cases {
		t.Run(tc.instName, func(t *testing.T) {
			collector := newOnceMockDataCollector([]libcluster.Data{
				{Source: "a", Value: cfg1},
				{Source: "b", Value: cfg2},
				{Source: "c", Value: cfg3},
			}, nil)
			publisher := newOnceMockDataPublisher(nil)
			picker := replicaset.KeyPicker(func(keys []string, _ bool) (int, error) {
				require.Equal(t, []string{tc.key}, keys)
				return 0, nil
			})
			source := replicaset.NewCConfigSource(collector, publisher, picker)
			err := source.Promote(replicaset.PromoteCtx{InstName: tc.instName})
			require.NoError(t, err)
			require.Equal(t, tc.key, publisher.Keys[0])
		})
	}
}
