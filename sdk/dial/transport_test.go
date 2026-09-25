package dial_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/dial"
)

func TestTransportType_String(t *testing.T) {
	cases := []struct {
		Transport dial.Transport
		Expected  string
	}{
		{dial.TransportDefault, ""},
		{dial.TransportPlain, "plain"},
		{dial.TransportSsl, "ssl"},
		{dial.Transport(15), "Transport(15)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			require.Equal(t, tc.Expected, tc.Transport.String())
		})
	}
}
