package cmd

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cluster"
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
			Name: "replicaset keys on the replicaset",
			Data: `replication:
  failover: manual
  bootstrap_strategy: config
groups:
  a:
    replicasets:
      b:
        leader: c1
        bootstrap_leader: c1
        instances:
          c1:
          c2:
`,
			Full: []bool{false, true},
			Err:  nil,
		},
		{
			Name: "leader on the top",
			Data: `leader: c
groups:
  a:
    replicasets:
      b:
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"leader [schema] No values are allowed because the schema is set to 'false'"},
		},
		{
			Name: "bootstrap leader on the top",
			Data: `bootstrap_leader: c
groups:
  a:
    replicasets:
      b:
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"bootstrap_leader [schema] No values are allowed"},
		},
		{
			Name: "leader on the group",
			Data: `groups:
  a:
    leader: c
    replicasets:
      b:
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"leader [schema] No values are allowed because the schema is set to 'false'"},
		},
		{
			Name: "leader on the instance",
			Data: `groups:
  a:
    replicasets:
      b:
        instances:
          c:
            leader: c
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"leader [schema] No values are allowed because the schema is set to 'false'"},
		},
		{
			Name: "bootstrap leader on the instance",
			Data: `groups:
  a:
    replicasets:
      b:
        instances:
          c:
            bootstrap_leader: c
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"bootstrap_leader [schema] No values are allowed"},
		},
		{
			Name: "leader in a section",
			Data: `replication:
  leader: c
groups:
  a:
    replicasets:
      b:
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"replication/leader [schema] No values are allowed"},
		},
		{
			Name: "leader of a wrong type",
			Data: `groups:
  a:
    replicasets:
      b:
        leader: 123
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"leader [type] invalid type"},
		},
		{
			Name: "unknown field next to the leader",
			Data: `groups:
  a:
    replicasets:
      b:
        leader: c
        foo: bar
        instances:
          c:
`,
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: ",
				"foo [schema] No values are allowed because the schema is set to 'false'",
				"an invalid instance \"c\" configuration: "},
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

	for _, testCase := range cases {
		for _, full := range testCase.Full {
			t.Run(testCase.Name+"_"+strconv.FormatBool(full), func(t *testing.T) {
				for k, v := range testCase.Env {
					t.Setenv(k, v)
				}

				view, err := cluster.BuildGoConfigFromBytes(context.Background(),
					[]byte(testCase.Data))
				require.NoError(t, err)

				err = validateGoConfig(view, full)

				for k := range testCase.Env {
					_ = os.Unsetenv(k)
				}

				if testCase.Err == nil {
					require.NoError(t, err)
				} else {
					for _, errStr := range testCase.Err {
						require.ErrorContains(t, err, errStr)
					}
				}
			})
		}
	}
}
