package tuple_test

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/tuple"
)

// luaPrintValues is the script that builds datetimes and intervals in
// Tarantool and prints, a line for each, the text Tarantool prints for it and
// its MessagePack in hex.
const luaPrintValues = `local datetime = require('datetime')
local msgpack = require('msgpack')
local values = {
    datetime.new{year = 2024, month = 11, day = 14, hour = 14, min = 2, sec = 36},
    datetime.new{year = 2024, month = 11, day = 14, nsec = 27000000, tz = 'Europe/Moscow'},
    datetime.new{year = 2024, month = 11, day = 14, nsec = 120000, tz = 'MSK'},
    datetime.new{year = 2024, month = 11, day = 14, nsec = 5, tzoffset = -90},
    datetime.new{year = 2024, month = 11, day = 14, tz = 'A'},
    datetime.new{year = 2024, month = 11, day = 14, tz = 'UTC'},
    datetime.new{year = -1, month = 1, day = 1},
    datetime.new{year = 12345, month = 6, day = 7, tzoffset = 840},
    -- Local mean times, whose offsets have seconds Tarantool drops.
    datetime.new{year = 1900, month = 1, day = 1, tz = 'Europe/Moscow'},
    datetime.new{year = 1900, month = 1, day = 1, hour = 12, tz = 'Europe/Dublin'},
    datetime.new{year = 1880, month = 1, day = 1, tz = 'America/New_York'},
    datetime.interval.new{},
    datetime.interval.new{nsec = 5},
    datetime.interval.new{year = 1, sec = -2},
    datetime.interval.new{month = -1, day = 3, hour = 4, min = 5, week = 6},
}
for _, v in ipairs(values) do
    io.write(tostring(v), '\t', (msgpack.encode(v):gsub('.', function(c)
        return string.format('%02x', c:byte())
    end)), '\n')
end
os.exit(0)
`

// TestTextIsTarantoolText checks that the Text of datetimes and intervals is
// the one Tarantool prints, and that ParseField reads that text back as the
// value Tarantool encodes.
func TestTextIsTarantoolText(t *testing.T) {
	t.Parallel()

	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found in PATH")
	}

	script := filepath.Join(t.TempDir(), "print_values.lua")
	require.NoError(t, os.WriteFile(script, []byte(luaPrintValues), 0o600))

	out, err := exec.CommandContext(t.Context(), tarantool, script).Output()
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.Len(t, lines, 15)

	for _, line := range lines {
		text, encoded, ok := strings.Cut(line, "\t")
		require.True(t, ok, line)

		raw, err := hex.DecodeString(encoded)
		require.NoError(t, err)

		value, err := tuple.Decode(raw)
		require.NoError(t, err)

		got, ok := tuple.Text(value)
		require.True(t, ok)
		assert.Equal(t, text, got)

		back, err := tuple.ParseField(text, tuple.Field{Type: tuple.FieldAny}, tuple.FieldOptions{})
		require.NoError(t, err, text)
		assertSameValue(t, value, back, text)
	}
}
