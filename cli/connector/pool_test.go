package connector_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/connector"
)

func TestConnectPool_failed_to_connect(t *testing.T) {
	cases := []struct {
		Name string
		Opts []connector.ConnectOpts
	}{
		{"nil", nil},
		{"empty", []connector.ConnectOpts{}},
		{"unreachetable", []connector.ConnectOpts{
			connector.ConnectOpts{
				Network: connector.TCPNetwork,
				Address: "unreachetable",
			},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			pool, err := connector.ConnectPool(tc.Opts)

			assert.Nil(t, pool)
			assert.EqualError(t, err, "failed to connect to any instance")
		})
	}
}
