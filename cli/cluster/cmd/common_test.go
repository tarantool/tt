package cmd

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	libcluster "github.com/tarantool/tt/lib/cluster"
)

func createClusterConfig(t *testing.T, data string) libcluster.ClusterConfig {
	t.Helper()

	config, err := libcluster.NewYamlCollector([]byte(data)).Collect()
	require.NoError(t, err)

	cconfig, err := libcluster.MakeClusterConfig(config)
	require.NoError(t, err)

	return cconfig
}

func TestValidateClusterConfig(t *testing.T) {
	cases := []struct {
		Name   string
		Env    map[string]string
		Config libcluster.ClusterConfig
		Full   []bool
		// The error order could be different.
		Err []string
	}{
		{
			Name:   "empty",
			Config: createClusterConfig(t, ``),
			Full:   []bool{false, true},
			Err:    nil,
		},
		{
			Name: "unknown fields",
			Config: createClusterConfig(t, `foo: bar
groups:
  a:
    foo: bar
    replicasets:
      b:
        foo: bar
        instances:
          c:
            foo: bar
`),
			Full: []bool{false, true},
			Err:  nil,
		},
		{
			Name: "valid fields",
			Config: createClusterConfig(t, `audit_log:
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
`),
			Full: []bool{false, true},
			Err:  nil,
		},
		{
			Name: "invalid base",
			Config: createClusterConfig(t, `audit_log:
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
`),
			Full: []bool{false, true},
			Err: []string{"an invalid cluster configuration: " +
				"invalid path \"audit_log.nonblock\": " +
				"unexpected value \"123\" of type int, expected boolean"},
		},
		{
			Name: "invalid group",
			Config: createClusterConfig(t, `audit_log:
  nonblock: true
groups:
  a:
    audit_log:
      nonblock: 123
    replicasets:
      b:
        instances:
          c:
`),
			Full: []bool{false, true},
			Err: []string{"an invalid instance \"c\" configuration: " +
				"invalid path \"audit_log.nonblock\": " +
				"unexpected value \"123\" of type int, expected boolean"},
		},
		{
			Name: "invalid replicaset",
			Config: createClusterConfig(t, `audit_log:
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
`),
			Full: []bool{false, true},
			Err: []string{"an invalid instance \"c\" configuration: " +
				"invalid path \"audit_log.nonblock\": " +
				"unexpected value \"123\" of type int, expected boolean"},
		},
		{
			Name: "invalid instance",
			Config: createClusterConfig(t, `audit_log:
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
`),
			Full: []bool{false, true},
			Err: []string{"an invalid instance \"c\" configuration: " +
				"invalid path \"audit_log.nonblock\": " +
				"unexpected value \"123\" of type int, expected boolean"},
		},
		{
			Name: "invalid instances",
			Config: createClusterConfig(t, `audit_log:
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
`),
			Full: []bool{false, true},
			Err: []string{
				"an invalid instance \"c1\" configuration: " +
					"invalid path \"audit_log.nonblock\": " +
					"unexpected value \"123\" of type int, expected boolean",
				"an invalid instance \"c2\" configuration: " +
					"invalid path \"audit_log.nonblock\": " +
					"unexpected value \"123\" of type int, expected boolean",
			},
		},
		{
			Name: "valid fields with env not full",
			Env: map[string]string{
				"TT_AUDIT_LOG_NONBLOCK": "123",
			},
			Config: createClusterConfig(t, `audit_log:
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
`),
			Full: []bool{false},
			Err:  nil,
		},
		{
			Name: "valid fields with env full",
			Env: map[string]string{
				"TT_AUDIT_LOG_NONBLOCK": "123",
			},
			Config: createClusterConfig(t, `audit_log:
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
`),
			Full: []bool{true},
			Err: []string{"an invalid instance \"c\" configuration: " +
				"invalid path \"audit_log.nonblock\": " +
				"unexpected value \"123\" of type string, expected boolean"},
		},
	}

	for _, tc := range cases {
		for _, full := range tc.Full {
			t.Run(tc.Name+"_"+fmt.Sprint(full), func(t *testing.T) {
				for k, v := range tc.Env {
					os.Setenv(k, v)
				}
				err := validateClusterConfig(tc.Config, full)
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
