package dial

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransportType_String(t *testing.T) {
	cases := []struct {
		Transport Transport
		Expected  string
	}{
		{TransportDefault, ""},
		{TransportPlain, "plain"},
		{TransportSsl, "ssl"},
		{Transport(15), "Transport(15)"},
	}

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			require.Equal(t, tc.Expected, tc.Transport.String())
		})
	}
}
