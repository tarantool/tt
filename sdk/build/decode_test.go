package build_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build/v3"
)

// overlay is a struct nested in a configuration.
type overlay struct {
	Cflags []string `toml:"cflags"`
}

// common is embedded into config to check that its fields are flattened.
type common struct {
	Jobs int `toml:"jobs"`
}

// config has a field of every kind the decoder fills.
type config struct {
	common

	Module    string             `toml:"module"`
	Sources   []string           `toml:"sources,omitempty"`
	Cgo       *bool              `toml:"cgo"`
	Level     uint8              `toml:"level"`
	Ratio     float64            `toml:"ratio"`
	Variables map[string]string  `toml:"variables"`
	Platforms map[string]overlay `toml:"platforms"`
	Extra     any                `toml:"extra"`
	Skipped   string             `toml:"-"`
	Untagged  string
	hidden    string
}

func backendOf(newConfig func() any) build.Backend {
	return build.Backend{
		Name:        "demo",
		Produces:    []build.Result{build.Module},
		Config:      newConfig,
		New:         nil,
		Incremental: false,
		OutputFrom:  build.OutputNone,
		Exclude:     nil,
	}
}

func TestFields(t *testing.T) {
	t.Parallel()

	fields, err := backendOf(func() any { return new(config) }).Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"jobs", "module", "sources", "cgo", "level", "ratio", "variables", "platforms",
		"extra", "Untagged",
	}, fields)
}

func TestDecode(t *testing.T) {
	t.Parallel()

	cfg, unknown, err := backendOf(func() any { return new(config) }).Decode(map[string]any{
		"jobs":      int64(4),
		"module":    "m",
		"sources":   []any{"a.c", "b.c"},
		"cgo":       true,
		"level":     int64(3),
		"ratio":     int64(2),
		"variables": map[string]any{"A": "1"},
		"platforms": map[string]any{
			"linux": map[string]any{"cflags": []any{"-O2"}, "ldflags": []any{"-s"}},
		},
		"extra":    []any{int64(1), "x"},
		"Untagged": "u",
		"make":     "all",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"make", "platforms.linux.ldflags"}, unknown)

	got, ok := cfg.(*config)
	require.True(t, ok)

	cgo := true

	assert.Equal(t, &config{
		common:    common{Jobs: 4}, //nolint:modernize // The embedded struct is the point.
		Module:    "m",
		Sources:   []string{"a.c", "b.c"},
		Cgo:       &cgo,
		Level:     3,
		Ratio:     2,
		Variables: map[string]string{"A": "1"},
		Platforms: map[string]overlay{"linux": {Cflags: []string{"-O2"}}},
		Extra:     []any{int64(1), "x"},
		Skipped:   "",
		Untagged:  "u",
		hidden:    "",
	}, got)
}

func TestDecodeFresh(t *testing.T) {
	t.Parallel()

	backend := backendOf(func() any { return new(config) })

	first, _, err := backend.Decode(map[string]any{"module": "a"})
	require.NoError(t, err)

	second, _, err := backend.Decode(nil)
	require.NoError(t, err)

	assert.Equal(t, &config{Module: "a"}, first) //nolint:exhaustruct // One field is set.
	assert.Equal(t, &config{}, second)           //nolint:exhaustruct // None is.
}

func TestDecodeTypeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rest  map[string]any
		field string
		msg   string
	}{
		{"a string for an array", map[string]any{"sources": "a.c"}, "sources",
			"must be an array, not a string"},
		{"an element", map[string]any{"sources": []any{"a.c", int64(1)}}, "sources[1]",
			"must be a string, not an integer"},
		{"a boolean", map[string]any{"cgo": "yes"}, "cgo", "must be a boolean, not a string"},
		{"a table", map[string]any{"variables": []any{}}, "variables",
			"must be a table, not an array"},
		{"a nested field", map[string]any{
			"platforms": map[string]any{"linux": map[string]any{"cflags": "-O2"}},
		}, "platforms.linux.cflags", "must be an array, not a string"},
		{"a struct", map[string]any{"platforms": map[string]any{"linux": true}},
			"platforms.linux", "must be a table, not a boolean"},
		{"an integer out of range", map[string]any{"level": int64(300)}, "level",
			"300 is out of the range of the field"},
		{"a negative unsigned", map[string]any{"level": int64(-1)}, "level",
			"-1 is out of the range of the field"},
		{"a float for an integer", map[string]any{"jobs": 1.5}, "jobs",
			"must be an integer, not a float"},
		{"a string for a number", map[string]any{"ratio": "1"}, "ratio",
			"must be a number, not a string"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := backendOf(func() any { return new(config) }).Decode(test.rest)

			var fieldErr *build.FieldError

			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, test.field, fieldErr.Field)
			assert.Equal(t, test.msg, fieldErr.Msg)
		})
	}
}

// recursive holds itself, which the decoder refuses.
type recursive struct {
	Next *recursive `toml:"next"`
}

func TestDecodeBadConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]func() any{
		"nil":                func() any { return nil },
		"not a pointer":      func() any { return config{} }, //nolint:exhaustruct // Empty.
		"a nil pointer":      func() any { return (*config)(nil) },
		"a pointer to a map": func() any { return &map[string]any{} },
		"a channel field":    func() any { return &struct{ C chan int }{} },
		"an int key":         func() any { return &struct{ M map[int]string }{} },
		"a recursive struct": func() any { return new(recursive) },
		"a key twice": func() any {
			return &struct {
				A string `toml:"x"`
				B string `toml:"x"`
			}{}
		},
	}

	for name, newConfig := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			backend := backendOf(newConfig)

			_, err := backend.Fields()
			require.ErrorIs(t, err, build.ErrConfig)

			_, _, err = backend.Decode(map[string]any{})
			require.ErrorIs(t, err, build.ErrConfig)
		})
	}

	_, err := backendOf(nil).Fields()
	require.ErrorIs(t, err, build.ErrConfig)
}
