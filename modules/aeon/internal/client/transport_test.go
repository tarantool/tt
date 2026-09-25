package client_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/aeon/internal/client"
)

func TestTransport_Set(t *testing.T) {
	tests := []struct {
		val     string
		want    client.Transport
		wantErr bool
	}{
		{"plain", client.Transport("plain"), false},
		{"ssl", client.Transport("ssl"), false},
		{"", client.Transport(""), true},
		{"mode", client.Transport(""), true},
	}
	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			var transport client.Transport

			err := transport.Set(tt.val)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.want, transport)
		})
	}
}

func TestTransport_Type(t *testing.T) {
	tests := []client.Transport{
		"plain",
		"ssl",
		"",
	}
	for _, tt := range tests {
		t.Run(string(tt), func(t *testing.T) {
			if got := tt.Type(); got != "MODE" {
				t.Errorf("Transport.Type() = %v, want MODE", got)
			}
		})
	}
}

func TestListValidTransports(t *testing.T) {
	ts := client.ListValidTransports()
	require.Equal(t, "[plain ssl]", ts)
}
