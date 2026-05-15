package cmd

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

func TestValidateGoConfig(t *testing.T) {
	cases := []struct {
		Name string
		Env  map[string]string
		Data string
		Full []bool
		// The error order could be different.
		Err []string
	}{
		{
			Name: "empty",
			Data: ``,
			Full: []bool{false, true},
			Err:  nil,
		},
		{
			Name: "unknown fields",
			Data: `foo: bar
groups:
  a:
    foo: bar
    replicasets:
      b:
        foo: bar
        instances:
          c:
            foo: bar
`,
			Full: []bool{false, true},
			Err: []string{
				"an invalid cluster configuration: ",
				"foo [schema] No values are allowed because the schema is set to 'false'",
			},
		},
		{
			Name: "valid fields",
			Data: `audit_log:
  nonblock: true
groups:
  a:
    audit_log:
      nonblock: true
    replicasets:
      b:
        audit_log:
          nonblock: true
        instances:
          c:
            audit_log:
              nonblock: true
`,
			Full: []bool{false, true},
			Err:  nil,
		},
		{
			Name: "invalid base",
			Data: `audit_log:
  nonblock: 123
groups:
  a:
    audit_log:
      nonblock: true
    replicasets:
      b:
        audit_log:
          nonblock: true
        instances:
          c:
            audit_log:
              nonblock: true
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"audit_log/nonblock [type] invalid type"},
		},
		{
			Name: "invalid group",
			Data: `audit_log:
  nonblock: true
groups:
  a:
    audit_log:
      nonblock: 123
    replicasets:
      b:
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid instance \"c\" configuration: ",
				"audit_log/nonblock [type] invalid type"},
		},
		{
			Name: "invalid replicaset",
			Data: `audit_log:
  nonblock: true
groups:
  a:
    audit_log:
      nonblock: true
    replicasets:
      b:
        audit_log:
          nonblock: 123
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid instance \"c\" configuration: ",
				"audit_log/nonblock [type] invalid type"},
		},
		{
			Name: "invalid instance",
			Data: `audit_log:
  nonblock: true
groups:
  a:
    audit_log:
      nonblock: true
    replicasets:
      b:
        audit_log:
          nonblock: true
        instances:
          c:
            audit_log:
              nonblock: 123
`,
			Full: []bool{false, true},
			Err: []string{"an invalid instance \"c\" configuration: ",
				"audit_log/nonblock [type] invalid type"},
		},
		{
			Name: "invalid instances",
			Data: `audit_log:
  nonblock: true
groups:
  a:
    audit_log:
      nonblock: true
    replicasets:
      b:
        audit_log:
          nonblock: true
        instances:
          c1:
            audit_log:
              nonblock: 123
          c2:
            audit_log:
              nonblock: 123
`,
			Full: []bool{false, true},
			Err: []string{
				"an invalid instance \"c1\" configuration: ",
				"an invalid instance \"c2\" configuration: ",
				"audit_log/nonblock [type] invalid type",
			},
		},
	}

	for _, tc := range cases {
		for _, full := range tc.Full {
			t.Run(tc.Name+"_"+fmt.Sprint(full), func(t *testing.T) {
				for k, v := range tc.Env {
					os.Setenv(k, v)
				}

				view, err := cluster.BuildGoConfigFromBytes(context.Background(), []byte(tc.Data))
				require.NoError(t, err)
				err = validateGoConfig(view, full)

				for k := range tc.Env {
					os.Unsetenv(k)
				}

				if tc.Err == nil {
					require.NoError(t, err)
				} else {
					for _, errStr := range tc.Err {
						require.ErrorContains(t, err, errStr)
					}
				}
			})
		}
	}
}
