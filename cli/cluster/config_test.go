package cluster_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

func Test_NewConfig(t *testing.T) {
	require.NotNil(t, cluster.NewConfig())
}

func TestConfig_Set(t *testing.T) {
	paths := [][][]string{
		[][]string{nil},
		[][]string{[]string{}},
		[][]string{[]string{"foo"}},
		[][]string{[]string{"foo", "bar"}},
		[][]string{[]string{"foo", "bar"}, []string{"foo", "zoo"}},
		[][]string{[]string{"foo", "bar", "baz"}, []string{"foo", "zoo"}},
	}

	for _, p := range paths {
		t.Run(fmt.Sprintf("%v", p), func(t *testing.T) {
			c := cluster.NewConfig()

			for i, cur := range p {
				c.Set(cur, i)
			}

			for i, cur := range p {
				got, err := c.Get(cur)

				assert.NoError(t, err)
				require.Equal(t, i, got)
			}
		})
	}
}

func TestConfig_Set_intersection(t *testing.T) {
	base := [][]string{
		nil,
		[]string{},
		[]string{"foo"},
		[]string{"foo", "bar"},
	}
	add := "zoo"

	for _, p := range base {
		t.Run(fmt.Sprintf("%v", p), func(t *testing.T) {
			c := cluster.NewConfig()

			err := c.Set(p, 1)
			assert.NoError(t, err)

			err = c.Set(append(p, add), 2)
			expected := fmt.Sprintf("path %q is not a map", p)
			require.EqualError(t, err, expected)
		})
	}
}

func TestConfig_Get_non_exist(t *testing.T) {
	paths := [][]string{
		[]string{"foo"},
		[]string{"zoo", "bar"},
	}

	c := cluster.NewConfig()
	err := c.Set([]string{"zoo", "foo"}, 1)
	require.NoError(t, err)

	for _, p := range paths {
		t.Run(fmt.Sprintf("%v", p), func(t *testing.T) {
			_, err := c.Get(p)
			expected := fmt.Sprintf("path %q does not exist", p)
			require.EqualError(t, err, expected)
			require.ErrorAs(t, err, &cluster.NotExistError{})
		})
	}
}

func TestConfig_Get_empty(t *testing.T) {
	c := cluster.NewConfig()
	_, err := c.Get(nil)
	require.EqualError(t, err, "path [] does not exist")
}

func TestConfig_Elems(t *testing.T) {
	c := cluster.NewConfig()
	err := c.Set([]string{"foo", "bar"}, 1)
	require.NoError(t, err)
	err = c.Set([]string{"foo", "car", "bar"}, 1)
	require.NoError(t, err)
	err = c.Set([]string{"foo", "zoo", "bar"}, 1)
	require.NoError(t, err)
	err = c.Set([]string{"car", "car", "bar"}, 1)
	require.NoError(t, err)

	expected := []string{"bar", "car", "zoo"}

	elems, err := c.Elems([]string{"foo"})
	require.NoError(t, err)
	assert.ElementsMatch(t, expected, elems)
}

func TestConfig_Elems_empty(t *testing.T) {
	c := cluster.NewConfig()
	_, err := c.Elems(nil)
	require.EqualError(t, err, "path [] does not exist")
}

func TestConfig_Elems_empty_not_map(t *testing.T) {
	c := cluster.NewConfig()
	err := c.Set(nil, "1")
	require.NoError(t, err)
	_, err = c.Elems(nil)
	require.EqualError(t, err, "path [] is not a map")
}

func TestConfig_Elems_not_map(t *testing.T) {
	c := cluster.NewConfig()
	err := c.Set([]string{"foo", "bar"}, "1")
	require.NoError(t, err)
	_, err = c.Elems([]string{"foo", "bar"})
	require.EqualError(t, err, "path [\"foo\" \"bar\"] is not a map")
}

func TestConfig_ForEach_nil(t *testing.T) {
	paths := [][]string{
		[]string{"foo", "bar"},
		[]string{"foo", "car"},
		[]string{"foo", "zoo", "nar"},
		[]string{"foo", "har", "mar", "nar"},
	}
	c := cluster.NewConfig()
	for _, p := range paths {
		err := c.Set(p, len(p))
		require.NoError(t, err)
	}

	foreachPaths := [][]string{}
	c.ForEach(nil, func(path []string, value any) {
		assert.Equal(t, len(path), value)
		foreachPaths = append(foreachPaths, path)
	})
	require.ElementsMatch(t, paths, foreachPaths)
}

func TestConfig_ForEach_path(t *testing.T) {
	paths := [][]string{
		[]string{"foo", "bar"},
		[]string{"foo", "car"},
		[]string{"foo", "zoo", "nar"},
		[]string{"foo", "zoo", "mar", "nar"},
	}
	c := cluster.NewConfig()
	for _, p := range paths {
		err := c.Set(p, len(p))
		require.NoError(t, err)
	}

	expected := [][]string{
		[]string{"foo", "zoo", "nar"},
		[]string{"foo", "zoo", "mar", "nar"},
	}
	foreachPaths := [][]string{}
	c.ForEach([]string{"foo", "zoo"}, func(path []string, value any) {
		assert.Equal(t, len(path), value)
		foreachPaths = append(foreachPaths, path)
	})
	require.ElementsMatch(t, expected, foreachPaths)
}

func TestConfig_ForEach_value(t *testing.T) {
	c := cluster.NewConfig()
	path := []string{"foo", "bar"}
	c.Set(path, 2)

	expected := [][]string{
		path,
	}
	foreachPaths := [][]string{}
	c.ForEach(path, func(path []string, value any) {
		assert.Equal(t, 2, value)
		foreachPaths = append(foreachPaths, path)
	})
	require.ElementsMatch(t, expected, foreachPaths)
}

func TestConfig_ForEach_map_value(t *testing.T) {
	const (
		key      = "bar"
		mapKey   = "foo"
		mapValue = 1
	)

	m := map[any]any{mapKey: mapValue}
	config := cluster.NewConfig()
	config.Set([]string{key}, m)

	paths := [][]string{}
	config.ForEach(nil, func(path []string, value any) {
		paths = append(paths, path)
		assert.Equal(t, mapValue, value)
	})
	require.Len(t, paths, 1)
	assert.Equal(t, []string{key, mapKey}, paths[0])
}

func TestConfig_ForEach_map_empty(t *testing.T) {
	const key = "foo"
	m := map[any]any{}
	config := cluster.NewConfig()
	config.Set([]string{key}, m)

	paths := [][]string{}
	config.ForEach(nil, func(path []string, value any) {
		paths = append(paths, path)
		assert.Equal(t, m, value)
	})
	require.Len(t, paths, 1)
	assert.Equal(t, []string{key}, paths[0])
}

func TestConfig_ForEach_empty(t *testing.T) {
	c := cluster.NewConfig()
	c.ForEach(nil, func(path []string, value any) {
		assert.Truef(t, false, "unexpected %v = %v", path, value)
	})
}

func TestConfig_Merge(t *testing.T) {
	left := cluster.NewConfig()
	err := left.Set([]string{"foo", "bar"}, 1)
	require.NoError(t, err)
	err = left.Set([]string{"foo", "zoo"}, 1)
	require.NoError(t, err)
	err = left.Set([]string{"car"}, 1)
	require.NoError(t, err)
	right := cluster.NewConfig()
	err = right.Set([]string{"foo", "bar"}, 2)
	require.NoError(t, err)
	err = right.Set([]string{"foo", "car"}, 2)
	require.NoError(t, err)
	err = right.Set([]string{"zoo"}, 2)
	require.NoError(t, err)
	expected := []struct {
		path  []string
		value any
	}{
		{[]string{"foo", "bar"}, 1},
		{[]string{"foo", "zoo"}, 1},
		{[]string{"foo", "car"}, 2},
		{[]string{"car"}, 1},
		{[]string{"zoo"}, 2},
	}

	left.Merge(right)

	for _, e := range expected {
		value, err := left.Get(e.path)
		assert.NoError(t, err)
		assert.Equal(t, e.value, value)
	}
}

func TestConfig_String(t *testing.T) {
	config := cluster.NewConfig()
	err := config.Set([]string{"foo", "bar"}, 1)
	require.NoError(t, err)
	err = config.Set([]string{"foo", "zoo"}, 1)
	require.NoError(t, err)
	err = config.Set([]string{"car"}, 1)
	require.NoError(t, err)
	err = config.Set([]string{"zoo"}, []string{"1", "2", "3"})
	require.NoError(t, err)

	expected := `car: 1
foo:
  bar: 1
  zoo: 1
zoo:
- "1"
- "2"
- "3"
`
	assert.Equal(t, expected, config.String())
}

func TestConfig_String_empty(t *testing.T) {
	config := cluster.NewConfig()
	assert.Equal(t, "", config.String())
}

func TestConfig_merge_empty_instances(t *testing.T) {
	data := `groups:
  a:
    replicasets:
      b:
        instances: {}
`
	config, err := cluster.NewYamlCollector([]byte(data)).Collect()
	require.NoError(t, err)

	mergeConfig := cluster.NewConfig()
	mergeConfig.Merge(config)

	assert.Equal(t, config.String(), mergeConfig.String())
	assert.Equal(t, data, mergeConfig.String())
}

func TestConfig_merge_data_and_empty_instances(t *testing.T) {
	data := `groups:
  a:
    replicasets:
      b:
        instances:
          c:
            foo: bar
`
	empty := `groups:
  a:
    replicasets:
      b:
        instances: {}
`
	cases := []struct {
		Name   string
		Config string
		Merge  string
	}{
		{"data_and_empty", data, empty},
		{"empty_and_data", empty, data},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := cluster.NewYamlCollector([]byte(tc.Config)).Collect()
			require.NoError(t, err)

			mergeConfig, err := cluster.NewYamlCollector([]byte(tc.Merge)).Collect()
			require.NoError(t, err)

			config.Merge(mergeConfig)
			assert.Equal(t, data, config.String())
		})
	}
}
