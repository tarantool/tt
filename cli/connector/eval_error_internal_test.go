package connector

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEvalErrorWithoutChunkPosition pins that the error of an eval over the
// console loses the position in the chunk of the eval, which Lua puts in
// front of an error raised at level 1, and keeps every other text.
func TestEvalErrorWithoutChunkPosition(t *testing.T) {
	for raw, expected := range map[string]string{
		`[string "local function func(...) local sharding = req..."]:1: ` +
			`Cluster is already bootstrapped`: "Cluster is already bootstrapped",
		`[string "local function func(...) error("x", 2) loca..."]:12: boom`: "boom",
		`Write access to space 'bands' is denied for user 'guest'`: `Write access to space ` +
			`'bands' is denied for user 'guest'`,
		`[string "box.cfg{}"]:1: not the chunk of an eval`: `[string "box.cfg{}"]:1: ` +
			`not the chunk of an eval`,
		`/opt/app/init.lua:3: an error of the application`: `/opt/app/init.lua:3: ` +
			`an error of the application`,
	} {
		require.Equal(t, expected, newEvaluationError(raw).Error(), raw)
	}

	// The YAML output of the console.
	err := getPlainTextEvalError([]byte("---\n- error: '[string \"local function func(...) "+
		"local x...\"]:1: sharding roles are not configured'\n...\n"), nil)
	require.EqualError(t, err, "sharding roles are not configured")

	// The Lua output of the console.
	_, err = getPlainTextEvalResLua([]byte(`"[string \"local function func(...) local x...\"]:1: ` +
		`failed to require vshard module";`))
	require.EqualError(t, err, "failed to require vshard module")
}
