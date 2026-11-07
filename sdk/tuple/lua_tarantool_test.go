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

// luaReadBack is the script that evaluates the expression in expr.lua and
// prints the MessagePack Tarantool encodes the value in, in hex.
const luaReadBack = `local v = dofile(arg[1])
io.write((require('msgpack').encode(v):gsub('.', function(c)
    return string.format('%02x', c:byte())
end)))
os.exit(0)
`

// TestAppendLuaReadsBackInTarantool evaluates the Lua of the sample tuple in
// Tarantool and checks that it builds the values decoded from it. A Lua
// number has no integral double, and a Lua table no order, so 3.0 comes back
// as 3 and the map is compared by its entries.
func TestAppendLuaReadsBackInTarantool(t *testing.T) {
	t.Parallel()

	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found in PATH")
	}

	dir := t.TempDir()
	expr := filepath.Join(dir, "expr.lua")
	script := filepath.Join(dir, "read_back.lua")

	items := decodeSample(t)
	require.NoError(t, os.WriteFile(expr,
		append([]byte("return "), tuple.AppendLua(nil, items)...), 0o600))
	require.NoError(t, os.WriteFile(script, []byte(luaReadBack), 0o600))

	out, err := exec.CommandContext(t.Context(), tarantool, script, expr).Output()
	require.NoError(t, err)

	raw, err := hex.DecodeString(strings.TrimSpace(string(out)))
	require.NoError(t, err)

	value, err := tuple.Decode(raw)
	require.NoError(t, err)

	back, ok := value.([]any)
	require.True(t, ok)
	require.Len(t, back, len(items))

	for index, item := range items {
		switch want := item.(type) {
		case tuple.Map:
			assert.ElementsMatch(t, want, back[index], "item %d", index)
		case nil, []any:
			assert.Equal(t, want, back[index], "item %d", index)
		default:
			wantText, _ := tuple.Text(want)
			gotText, _ := tuple.Text(back[index])
			assert.Equal(t, wantText, gotText, "item %d", index)
		}
	}
}
