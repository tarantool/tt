package cc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClashingSymbols pins which exports of a module clash with those of
// Tarantool: each once, sorted, but the entry point of the module.
func TestClashingSymbols(t *testing.T) {
	t.Parallel()

	tarantool := []string{"curl_easy_init", "lua_pushnumber", "luaopen_bit", "crc32"}

	assert.Equal(t, []string{"crc32", "curl_easy_init"}, clashingSymbols(
		[]string{"curl_easy_init", "mine", "crc32", "curl_easy_init", "luaopen_bit"},
		tarantool, "luaopen_bit"))
	assert.Empty(t, clashingSymbols([]string{"mine", "luaopen_bit"}, tarantool, "luaopen_bit"))
	assert.Empty(t, clashingSymbols([]string{"crc32"}, nil, ""))
}

// TestClashWarning pins the warning of clashes: the module, the tarantool,
// up to shownClashes names, and the fix.
func TestClashWarning(t *testing.T) {
	t.Parallel()

	warning := clashWarning("/lib/m.so", "/opt/tarantool", []string{"a", "b"})
	assert.Contains(t, warning, "/lib/m.so exports 2 symbols that the tarantool "+
		"/opt/tarantool exports too: a, b;")
	assert.Contains(t, warning, "make them static, or hidden")

	warning = clashWarning("/lib/m.so", "/opt/tarantool",
		[]string{"a", "b", "c", "d", "e", "f", "g"})
	assert.Contains(t, warning, "exports 7 symbols")
	assert.Contains(t, warning, ": a, b, c, d, e and 2 more;")
}

// TestExportedSymbolsOfNoBinary checks that a file of no format of a binary
// is an error, which the check of clashes skips.
func TestExportedSymbolsOfNoBinary(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "tarantool")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho Tarantool 3.3.5\n"), 0o600))

	_, err := exportedSymbols(path)
	require.ErrorIs(t, err, errUnknownFormat)

	_, err = exportedSymbols(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
