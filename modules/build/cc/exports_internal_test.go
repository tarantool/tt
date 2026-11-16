package cc

import (
	"os"
	"path/filepath"
	"strings"
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

// fakeTarantool is a tarantool that records each run as a line of
// $TT_BUILD_DIR/loads - the module and the symbol of the load - and prints
// what the file $TT_BUILD_DIR/load says, the result of the load script.
const fakeTarantool = `#!/bin/sh
printf '%s %s\n' "$TT_CC_LOAD_MODULE" "$TT_CC_LOAD_SYMBOL" >> "$TT_BUILD_DIR/loads"
cat "$TT_BUILD_DIR/load"
`

// TestRunLoadsTheModule checks that a module just linked is loaded into the
// tarantool of the build, by its entry point for lua-c, and that a module up
// to date is not; that a failed load fails the build with what the
// loader said, and that a tarantool that does not run the script loads
// nothing.
func TestRunLoadsTheModule(t *testing.T) {
	t.Parallel()

	harness, bld := fakeHarness(t, LuaC(), map[string]any{"module": "m", "sources": []any{"a.c"}},
		fakeCompilers())
	work := harness.Context.Dirs.Work
	tarantool := filepath.Join(t.TempDir(), "tarantool")
	module := filepath.Join(harness.Context.Dirs.Modules, "m"+harness.Context.Target.ModuleExt())

	writeProgram(t, tarantool, fakeTarantool)
	writeFile(t, harness, "a.c", "/* a.c */\n")

	// The fake module is no ELF, which checkLuaModule does not check.
	harness.Context.Tarantool.Executable = tarantool

	loads := func() []string {
		data, err := os.ReadFile(filepath.Join(work, "loads"))
		if err != nil {
			return nil
		}

		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	result := func(printed string) {
		require.NoError(t, os.WriteFile(filepath.Join(work, "load"), []byte(printed), 0o600))
	}

	result("tt-cc-load: ok\n")
	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Equal(t, []string{module + " luaopen_demo_m"}, loads(), "required as demo.m")

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Len(t, loads(), 1, "a module up to date is not loaded again")

	writeFile(t, harness, "a.c", "/* a.c changed */\n")
	result("tt-cc-load: failed\n" + module + ": undefined symbol: deflate\n")

	err := harness.Run(t.Context(), bld)
	require.ErrorIs(t, err, errNoLoad)
	assert.Contains(t, err.Error(), module+": the module does not load into "+tarantool+": "+
		module+": undefined symbol: deflate")

	// The module that did not load is linked and loaded again.
	result("Tarantool 3.3.5\n")
	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.Len(t, loads(), 3, "a tarantool that does not run the script loads nothing")

	harness.Context.Tarantool.Executable = filepath.Join(t.TempDir(), "missing")

	writeFile(t, harness, "a.c", "/* a.c changed again */\n")
	require.NoError(t, harness.Run(t.Context(), bld), "a tarantool that is not there")
}

// TestLoadSymbol pins the symbol a module is loaded by.
func TestLoadSymbol(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "*", loadSymbol(""))
	assert.Equal(t, "luaopen_app_fast", loadSymbol("my-app.fast"))
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
