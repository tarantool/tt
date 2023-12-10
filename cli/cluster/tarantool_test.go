package cluster_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/connector"
)

type MockEvaler struct {
	connector.Evaler
	Expr string
	Args []any
	Opts connector.RequestOpts
	Ret  []any
	Err  error
}

func (m *MockEvaler) Eval(expr string, args []any,
	opts connector.RequestOpts) ([]any, error) {
	m.Expr = expr
	m.Args = args
	m.Opts = opts
	return m.Ret, m.Err
}

func TestNewTarantoolCollectors(t *testing.T) {
	cases := []struct {
		Name      string
		Collector cluster.Collector
	}{
		{"any", cluster.NewTarantoolAllCollector(&MockEvaler{}, "", 0)},
		{"key", cluster.NewTarantoolKeyCollector(&MockEvaler{}, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.NotNil(t, tc.Collector)
		})
	}
}

func TestNewTarantoolCollectors_Collect_nil_evaler(t *testing.T) {
	cases := []struct {
		Name      string
		Collector cluster.Collector
	}{
		{"any", cluster.NewTarantoolAllCollector(nil, "", 0)},
		{"key", cluster.NewTarantoolKeyCollector(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Collector.Collect()
			})
		})
	}
}

func TestTarantoolCollectors_Collect_request_error(t *testing.T) {
	mock := &MockEvaler{
		Err: fmt.Errorf("any"),
	}
	cases := []struct {
		Name      string
		Collector cluster.Collector
	}{
		{"all", cluster.NewTarantoolAllCollector(mock, "/foo", 0)},
		{"key", cluster.NewTarantoolKeyCollector(mock, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()

			assert.ErrorContains(t, err, "failed to fetch data from tarantool: any")
			assert.Nil(t, config)
		})
	}
}

func TestTarantoolCollectors_Collect_empty(t *testing.T) {
	mock := &MockEvaler{
		Ret: nil,
	}
	cases := []struct {
		Name      string
		Collector cluster.Collector
	}{
		{"all", cluster.NewTarantoolAllCollector(mock, "/foo", 0)},
		{"key", cluster.NewTarantoolKeyCollector(mock, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()
			assert.EqualError(t, err, "unexpected response from tarantool: []")
			assert.Nil(t, config)
		})
	}
}

func TestNewTarantoolAllCollector_Collect_request_inputs(t *testing.T) {
	expectedExpr := "return config.storage.get(...)"
	cases := []struct {
		Prefix       string
		Timeout      time.Duration
		ExpectedArgs []any
	}{
		{
			Prefix:       "",
			Timeout:      0,
			ExpectedArgs: []any{"/config/"},
		},
		{
			Prefix:       "////",
			Timeout:      1,
			ExpectedArgs: []any{"/config/"},
		},
		{
			Prefix:       "foo",
			Timeout:      2,
			ExpectedArgs: []any{"foo/config/"},
		},
		{
			Prefix:       "/foo/bar",
			Timeout:      3,
			ExpectedArgs: []any{"/foo/bar/config/"},
		},
		{
			Prefix:       "/foo/bar///",
			Timeout:      4,
			ExpectedArgs: []any{"/foo/bar/config/"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Prefix, func(t *testing.T) {
			expectedOpts := connector.RequestOpts{ReadTimeout: tc.Timeout}
			evaler := &MockEvaler{Err: fmt.Errorf("foo")}

			collector := cluster.NewTarantoolAllCollector(evaler, tc.Prefix, tc.Timeout)
			collector.Collect()

			assert.Equal(t, expectedExpr, evaler.Expr)
			assert.Equal(t, tc.ExpectedArgs, evaler.Args)
			assert.Equal(t, expectedOpts, evaler.Opts)
		})
	}
}

func TestNewTarantoolAllCollector_Collect_data_errors(t *testing.T) {
	cases := []struct {
		Name     string
		Ret      []any
		Expected string
	}{
		{
			Name:     "too many responses",
			Ret:      []any{"foo", "bar"},
			Expected: "unexpected response from tarantool",
		},
		{
			Name:     "invalid response",
			Ret:      []any{"foo"},
			Expected: "failed to map response from tarantool",
		},
		{
			Name: "empty data",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{},
				},
			},
			Expected: "a configuration data not found in tarantool",
		},
		{
			Name: "invalid data yaml",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{
						map[any]any{
							"path":  "foo",
							"value": "f: a\n- b\n",
						},
					},
				},
			},
			Expected: "failed to decode tarantool config for key \"foo\"",
		},
		{
			Name: "invalid second data yaml",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{
						map[any]any{
							"path":  "bar",
							"value": "f: a\nb: a\n",
						},
						map[any]any{
							"path":  "foo",
							"value": "f: a\n- b\n",
						},
					},
				},
			},
			Expected: "failed to decode tarantool config for key \"foo\"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evaler := &MockEvaler{Ret: tc.Ret}

			collector := cluster.NewTarantoolAllCollector(evaler, "", 0)
			config, err := collector.Collect()

			assert.Nil(t, config)
			assert.ErrorContains(t, err, tc.Expected)
		})
	}
}

func TestNewTarantoolAllCollector_Collect(t *testing.T) {
	cases := []struct {
		Name     string
		Ret      []any
		Expected string
	}{
		{
			Name: "single",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{
						map[any]any{
							"path":  "foo",
							"value": "f: a\nb: a\n",
						},
					},
				},
			},
			Expected: "b: a\nf: a\n",
		},
		{
			Name: "multiple",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{
						map[any]any{
							"path":  "foo",
							"value": "f: a\nb: a\n",
						},
						map[any]any{
							"path":  "bar",
							"value": "f: b\nb: b\nc: b\n",
						},
					},
				},
			},
			Expected: "b: a\nc: b\nf: a\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evaler := &MockEvaler{Ret: tc.Ret}

			collector := cluster.NewTarantoolAllCollector(evaler, "", 0)
			config, err := collector.Collect()

			require.NoError(t, err)
			assert.Equal(t, tc.Expected, config.String())
		})
	}
}

func TestNewTarantoolKeyCollector_Collect_request_inputs(t *testing.T) {
	expectedExpr := "return config.storage.get(...)"
	cases := []struct {
		Prefix       string
		Key          string
		Timeout      time.Duration
		ExpectedArgs []any
	}{
		{
			Prefix:       "",
			Key:          "",
			Timeout:      0,
			ExpectedArgs: []any{"/config/"},
		},
		{
			Prefix:       "////",
			Key:          "foo",
			Timeout:      1,
			ExpectedArgs: []any{"/config/foo"},
		},
		{
			Prefix:       "foo",
			Key:          "//foo//",
			Timeout:      2,
			ExpectedArgs: []any{"foo/config///foo//"},
		},
		{
			Prefix:       "/foo/bar",
			Key:          "bar",
			Timeout:      3,
			ExpectedArgs: []any{"/foo/bar/config/bar"},
		},
		{
			Prefix:       "/foo/bar///",
			Key:          "/foo/bar",
			Timeout:      4,
			ExpectedArgs: []any{"/foo/bar/config//foo/bar"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Prefix, func(t *testing.T) {
			expectedOpts := connector.RequestOpts{ReadTimeout: tc.Timeout}
			evaler := &MockEvaler{Err: fmt.Errorf("foo")}

			collector := cluster.NewTarantoolKeyCollector(evaler, tc.Prefix, tc.Key, tc.Timeout)
			collector.Collect()

			assert.Equal(t, expectedExpr, evaler.Expr)
			assert.Equal(t, tc.ExpectedArgs, evaler.Args)
			assert.Equal(t, expectedOpts, evaler.Opts)
		})
	}
}

func TestNewTarantoolKeyCollector_Collect_data_errors(t *testing.T) {
	cases := []struct {
		Name     string
		Ret      []any
		Expected string
	}{
		{
			Name:     "too many responses",
			Ret:      []any{"foo", "bar"},
			Expected: "unexpected response from tarantool",
		},
		{
			Name:     "invalid response",
			Ret:      []any{"foo"},
			Expected: "failed to map response from tarantool",
		},
		{
			Name: "empty data",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{},
				},
			},
			Expected: "a configuration data not found in tarantool",
		},
		{
			Name: "invalid data yaml",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{
						map[any]any{
							"path":  "foo",
							"value": "f: a\n- b\n",
						},
					},
				},
			},
			Expected: "failed to decode tarantool config for key \"/config/\"",
		},
		{
			Name: "too many data",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{
						map[any]any{
							"path":  "bar",
							"value": "f: a\nb: a\n",
						},
						map[any]any{
							"path":  "foo",
							"value": "f: a\nb: a\n",
						},
					},
				},
			},
			Expected: "too many responses",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evaler := &MockEvaler{Ret: tc.Ret}

			collector := cluster.NewTarantoolKeyCollector(evaler, "", "", 0)
			config, err := collector.Collect()

			assert.Nil(t, config)
			assert.ErrorContains(t, err, tc.Expected)
		})
	}
}

func TestNewTarantoolKeyCollector_Collect(t *testing.T) {
	cases := []struct {
		Name     string
		Ret      []any
		Expected string
	}{
		{
			Name: "single",
			Ret: []any{
				map[any]any{
					"data": []map[any]any{
						map[any]any{
							"path":  "foo",
							"value": "f: a\nb: a\n",
						},
					},
				},
			},
			Expected: "b: a\nf: a\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			evaler := &MockEvaler{Ret: tc.Ret}

			collector := cluster.NewTarantoolKeyCollector(evaler, "", "", 0)
			config, err := collector.Collect()

			require.NoError(t, err)
			assert.Equal(t, tc.Expected, config.String())
		})
	}
}

func TestNewTarantoolDataPublishers(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewTarantoolAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewTarantoolKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.NotNil(t, tc.Publisher)
		})
	}
}

func TestNewTarantoolDataPublishers_Publish_nil_evaler(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewTarantoolAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewTarantoolKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Publisher.Publish([]byte{})
			})
		})
	}
}

func TestTarantoolPublishers_Publish_request_error(t *testing.T) {
	mock := &MockEvaler{
		Err: fmt.Errorf("any"),
	}
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewTarantoolAllDataPublisher(mock, "/foo", 0)},
		{"key", cluster.NewTarantoolKeyDataPublisher(mock, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := tc.Publisher.Publish([]byte{})

			assert.ErrorContains(t, err, "failed to put data into tarantool: any")
		})
	}
}

func TestTarantoolPublishers_Publish_request_ok(t *testing.T) {
	mock := &MockEvaler{
		Ret: []any{"any", "does not matter"},
	}
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewTarantoolAllDataPublisher(mock, "/foo", 0)},
		{"key", cluster.NewTarantoolKeyDataPublisher(mock, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := tc.Publisher.Publish([]byte{})

			assert.NoError(t, err)
		})
	}
}

func TestTarantoolAllDataPublisher_Publish_request_inputs(t *testing.T) {
	expectedExpr := "return config.storage.txn(...)"
	cases := []struct {
		Prefix       string
		Timeout      time.Duration
		Data         []byte
		ExpectedArgs []any
	}{
		{
			Prefix:  "",
			Timeout: 0,
			Data:    nil,
			ExpectedArgs: []any{
				map[any]any{
					"on_success": []any{
						[]any{"delete", "/config/"},
						[]any{"put", "/config/all", ""},
					},
				},
			},
		},
		{
			Prefix:  "foo",
			Timeout: 1,
			Data:    []byte("string"),
			ExpectedArgs: []any{
				map[any]any{
					"on_success": []any{
						[]any{"delete", "foo/config/"},
						[]any{"put", "foo/config/all", "string"},
					},
				},
			},
		},
		{
			Prefix:  "//////",
			Timeout: 2,
			Data:    []byte("any"),
			ExpectedArgs: []any{
				map[any]any{
					"on_success": []any{
						[]any{"delete", "/config/"},
						[]any{"put", "/config/all", "any"},
					},
				},
			},
		},
		{
			Prefix:  "/foo/bar",
			Timeout: 3,
			Data:    []byte("any"),
			ExpectedArgs: []any{
				map[any]any{
					"on_success": []any{
						[]any{"delete", "/foo/bar/config/"},
						[]any{"put", "/foo/bar/config/all", "any"},
					},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.Prefix, func(t *testing.T) {
			expectedOpts := connector.RequestOpts{ReadTimeout: tc.Timeout}
			evaler := &MockEvaler{}

			publisher := cluster.NewTarantoolAllDataPublisher(evaler,
				tc.Prefix, tc.Timeout)
			publisher.Publish(tc.Data)

			assert.Equal(t, expectedExpr, evaler.Expr)
			assert.Equal(t, tc.ExpectedArgs, evaler.Args)
			assert.Equal(t, expectedOpts, evaler.Opts)
		})
	}
}

func TestTarantoolKeyDataPublisher_Publish_request_inputs(t *testing.T) {
	expectedExpr := "return config.storage.put(...)"
	cases := []struct {
		Prefix       string
		Key          string
		Timeout      time.Duration
		Data         []byte
		ExpectedArgs []any
	}{
		{
			Prefix:       "",
			Key:          "",
			Timeout:      0,
			Data:         nil,
			ExpectedArgs: []any{"/config/", ""},
		},
		{
			Prefix:       "/foo///",
			Key:          "bar",
			Timeout:      1,
			Data:         []byte("any"),
			ExpectedArgs: []any{"/foo/config/bar", "any"},
		},
		{
			Prefix:       "/foo///",
			Key:          "bar/bar/foo",
			Timeout:      2,
			Data:         []byte("any"),
			ExpectedArgs: []any{"/foo/config/bar/bar/foo", "any"},
		},
		{
			Prefix:       "/foo///",
			Key:          "bar/bar/foo//",
			Timeout:      2,
			Data:         []byte("any"),
			ExpectedArgs: []any{"/foo/config/bar/bar/foo//", "any"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.Prefix, func(t *testing.T) {
			expectedOpts := connector.RequestOpts{ReadTimeout: tc.Timeout}
			evaler := &MockEvaler{}

			publisher := cluster.NewTarantoolKeyDataPublisher(evaler,
				tc.Prefix, tc.Key, tc.Timeout)
			publisher.Publish(tc.Data)

			assert.Equal(t, expectedExpr, evaler.Expr)
			assert.Equal(t, tc.ExpectedArgs, evaler.Args)
			assert.Equal(t, expectedOpts, evaler.Opts)
		})
	}
}
