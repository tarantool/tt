package connector

import (
	"bytes"
	"encoding/hex"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvalErrorWithoutChunkPosition pins that the error of an eval over the
// console loses the position in the chunk of the eval, which Lua puts in
// front of an error raised at level 1, and keeps every other text.
func TestEvalErrorWithoutChunkPosition(t *testing.T) {
	for raw, expected := range map[string]string{
		`eval:1: Cluster is already bootstrapped`: "Cluster is already bootstrapped",
		`eval:12: boom`: "boom",
		`[string "local func, err = load(string.fromhex('6572726f..."]:1: at level 2`: "at level 2",
		`Write access to space 'bands' is denied for user 'guest'`: `Write access to space ` +
			`'bands' is denied for user 'guest'`,
		`[string "box.cfg{}"]:1: not the chunk of an eval`: `[string "box.cfg{}"]:1: ` +
			`not the chunk of an eval`,
		`/opt/app/init.lua:3: an error of the application`: `/opt/app/init.lua:3: ` +
			`an error of the application`,
		`evaluation:1: another chunk`: `evaluation:1: another chunk`,
	} {
		require.Equal(t, expected, newEvaluationError(raw).Error(), raw)
	}

	// The YAML output of the console.
	err := getPlainTextEvalError([]byte("---\n- error: 'eval:1: sharding roles are not "+
		"configured'\n...\n"), nil)
	require.EqualError(t, err, "sharding roles are not configured")

	// The Lua output of the console.
	_, err = getPlainTextEvalResLua([]byte(`"eval:3: failed to require vshard module";`))
	require.EqualError(t, err, "failed to require vshard module")
}

// TestFormatEvalFuncBody pins the line the console is sent for an eval: one
// line, which carries the body in hex, so that a body of several lines with
// line comments, quotes and brackets reaches Lua as it is written.
func TestFormatEvalFuncBody(t *testing.T) {
	body := "-- a comment first\nlocal a = ... -- the argument\n" +
		"return a .. ']]' .. \"'\" -- and the end\n"

	client, server := net.Pipe()

	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	sent := make(chan string, 1)

	go func() {
		var line bytes.Buffer

		chunk := make([]byte, 4096)

		for !strings.HasSuffix(line.String(), "\n") {
			n, err := server.Read(chunk)
			if err != nil {
				break
			}

			line.Write(chunk[:n])
		}

		sent <- line.String()
	}()

	require.NoError(t, formatAndSendEvalFunc(client, body, []any{"x"}, evalFuncTmpl))

	line := <-sent
	assert.Equal(t, 1, strings.Count(line, "\n"), line)
	assert.True(t, strings.HasSuffix(line, "\n"), line)
	assert.NotContains(t, line, "--", "the body is not inlined")
	assert.Contains(t, line, hex.EncodeToString([]byte(body)))
}
