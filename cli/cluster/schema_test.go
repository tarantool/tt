package cluster_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

func TestValidate_ok(t *testing.T) {
	config := cluster.NewConfig()
	schema := []cluster.SchemaPath{
		{
			Path:      []string{"foo", "number"},
			Validator: cluster.NumberValidator{},
		},
		{
			Path:      []string{"zoo", "number"},
			Validator: cluster.NumberValidator{},
		},
		{
			Path:      []string{"bar", "number"},
			Validator: cluster.NumberValidator{},
		},
	}

	err := config.Set([]string{"foo", "number"}, 1)
	require.NoError(t, err)
	err = config.Set([]string{"zoo", "number"}, 2)
	require.NoError(t, err)
	err = config.Set([]string{"bar", "number"}, "1")
	require.NoError(t, err)

	err = cluster.Validate(config, schema)
	require.NoError(t, err)
}

func TestValidate_errors(t *testing.T) {
	config := cluster.NewConfig()
	schema := []cluster.SchemaPath{
		{
			Path:      []string{"foo", "number"},
			Validator: cluster.NumberValidator{},
		},
		{
			Path:      []string{"zoo", "number"},
			Validator: cluster.NumberValidator{},
		},
		{
			Path:      []string{"bar", "number"},
			Validator: cluster.NumberValidator{},
		},
	}
	expected := []struct {
		Path []string
		Errs []error
	}{
		{
			Path: []string{"foo", "number"},
			Errs: []error{fmt.Errorf("failed to parse value \"foo\" to type number")},
		},
		{
			Path: []string{"zoo", "number"},
			Errs: []error{fmt.Errorf("unexpected value \"false\" of type bool, expected number")},
		},
	}

	err := config.Set([]string{"foo", "number"}, "foo")
	require.NoError(t, err)
	err = config.Set([]string{"zoo", "number"}, false)
	require.NoError(t, err)
	err = config.Set([]string{"bar", "number"}, "1")
	require.NoError(t, err)

	err = cluster.Validate(config, schema)
	require.EqualError(t, err, "invalid path \"foo.number\": "+
		"failed to parse value \"foo\" to type number\n"+
		"invalid path \"zoo.number\": "+
		"unexpected value \"false\" of type bool, expected number")
	errs := err.(interface{ Unwrap() []error }).Unwrap()
	require.Equal(t, len(expected), len(errs))

	for i, e := range errs {
		validateErr := e.(cluster.ValidateError)
		assert.Equal(t, expected[i].Path, validateErr.Path())
		assert.Equal(t, expected[i].Errs, validateErr.Unwrap())
	}
}

func TestValidate_instance_schema(t *testing.T) {
	config := cluster.NewConfig()
	err := config.Set([]string{"config", "etcd", "ssl", "verify_host"}, false)
	require.NoError(t, err)
	err = config.Set([]string{"config", "etcd", "ssl", "verify_peer"}, "true")
	require.NoError(t, err)
	err = config.Set([]string{"config", "reload"}, "auto")
	require.NoError(t, err)
	err = config.Set([]string{"console", "enabled"}, true)
	require.NoError(t, err)
	err = config.Set([]string{"credentials", "roles"}, map[any]any{
		"foo": map[any]any{
			"privileges": []any{
				map[any]any{
					"permissions": []any{"read", "sessions"},
					"universe":    true,
				},
			},
			"roles": []any{"foo", "bar"},
		},
	})

	err = cluster.Validate(config, cluster.TarantoolSchema)
	unwrap := err.(interface{ Unwrap() []error })
	require.Len(t, unwrap.Unwrap(), 1)

	var validateErr cluster.ValidateError
	require.ErrorAs(t, err, &validateErr)
	assert.Equal(t, []string{"credentials", "roles", "foo", "privileges",
		"permissions"}, validateErr.Path())

	require.Len(t, validateErr.Unwrap(), 1)
	assert.EqualError(t, validateErr.Unwrap()[0],
		"value \"sessions\" should be one of "+
			"[read write execute create alter drop usage session]")
}
