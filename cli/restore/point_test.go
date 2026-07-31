package restore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePoint_Valid(t *testing.T) {
	point, err := ParsePoint(`{"replica_id":1,"lsn":1502}`)
	require.NoError(t, err)

	require.Equal(t, &Point{ReplicaID: 1, LSN: 1502}, point)
}

func TestParsePoint_Rejects(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "not json", raw: "1:1502"},
		{name: "no replica_id", raw: `{"lsn":1502}`},
		{name: "no lsn", raw: `{"replica_id":1}`},
		{name: "replica 0", raw: `{"replica_id":0,"lsn":1502}`},
		// A misspelled key would otherwise decode to replica_id 0 and trim
		// on the wrong axis without a word.
		{name: "misspelled key", raw: `{"replicaid":1,"lsn":1502}`},
		{name: "extra key", raw: `{"replica_id":1,"lsn":1502,"name":"x"}`},
		{name: "negative lsn", raw: `{"replica_id":1,"lsn":-1}`},
		{name: "lsn above int64", raw: `{"replica_id":1,"lsn":18446744073709551615}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePoint(tc.raw)
			require.ErrorIs(t, err, ErrValidation)
		})
	}
}
