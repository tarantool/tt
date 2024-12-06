package cmd_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/aeon/cmd"
)

func TestTransport_Set(t *testing.T) {
	tests := []struct {
		val     string
		want    cmd.Transport
		wantErr bool
	}{
		{"plain", cmd.Transport("plain"), false},
		{"ssl", cmd.Transport("ssl"), false},
		{"", cmd.Transport(""), true},
		{"mode", cmd.Transport(""), true},
	}
	for _, tt := range tests {
		t.Run(string(tt.val), func(t *testing.T) {
			var tr cmd.Transport
			if err := tr.Set(tt.val); (err != nil) != tt.wantErr {
				t.Errorf("Transport.Set() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTransport_Type(t *testing.T) {
	tests := []cmd.Transport{
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
	ts := cmd.ListValidTransports()
	require.Equal(t, "[plain ssl]", ts)
}
