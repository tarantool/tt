package tuple_test

import (
	"encoding/hex"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/tuple"
)

// luaReadBack is the script that evaluates the expression in expr.lua and
// prints the MessagePack Tarantool encodes the value in, in hex.
const luaReadBack = `local v = dofile(arg[1])
io.write((require('msgpack').encode(v):gsub('.', function(c)
    return string.format('%02x', c:byte())
end)))
os.exit(0)
`

// TestAppendLuaReadsBackInTarantool evaluates the Lua of the sample tuple and
// of values Lua has no literal for in Tarantool and checks that it builds the
// MessagePack they were decoded from. A Lua table has no order, so a map is
// compared by its entries.
func TestAppendLuaReadsBackInTarantool(t *testing.T) {
	t.Parallel()

	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found in PATH")
	}

	items := decodeSample(t)

	// A datetime of no time zone in UTC, which Text writes with a Z, an
	// integral double and -0, a NaN float of a sign 0/0 does not give, and a
	// map of an integral double key.
	for _, raw := range []string{"d7048000926500000000", "cb3ff0000000000000",
		"cb8000000000000000", "ca7fc00000", "81cb4000000000000000a178"} {
		items = append(items, decodeHex(t, raw))
	}

	items = append(items, math.Copysign(0, -1))

	back := readBackLua(t, tarantool, items)
	require.Len(t, back, len(items))

	for index, item := range items {
		if want, ok := item.(tuple.Map); ok {
			assert.ElementsMatch(t, want, back[index], "item %d", index)

			continue
		}

		want, err := tuple.AppendMsgpack(nil, item)
		require.NoError(t, err)

		got, err := tuple.AppendMsgpack(nil, back[index])
		require.NoError(t, err)

		assert.Equal(t, hex.EncodeToString(want), hex.EncodeToString(got), "item %d: %s",
			index, tuple.AppendLua(nil, item))
	}
}

// readBackLua evaluates the Lua of items in tarantool and decodes the
// MessagePack of what it builds.
func readBackLua(t *testing.T, tarantool string, items []any) []any {
	t.Helper()

	dir := t.TempDir()
	expr := filepath.Join(dir, "expr.lua")
	script := filepath.Join(dir, "read_back.lua")

	require.NoError(t, os.WriteFile(expr,
		append([]byte("return "), tuple.AppendLua(nil, items)...), 0o600))
	require.NoError(t, os.WriteFile(script, []byte(luaReadBack), 0o600))

	out, err := exec.CommandContext(t.Context(), tarantool, script, expr).Output()
	require.NoError(t, err)

	value := decodeHex(t, strings.TrimSpace(string(out)))

	back, ok := value.([]any)
	require.True(t, ok)

	return back
}

func decodeHex(t *testing.T, text string) any {
	t.Helper()

	raw, err := hex.DecodeString(text)
	require.NoError(t, err)

	value, err := tuple.Decode(raw)
	require.NoError(t, err)

	return value
}
