package cc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLuaEntry pins the entry point require calls, as LuaJIT's mksymname
// names it.
func TestLuaEntry(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"fast":          "luaopen_fast",
		"demo.fast":     "luaopen_demo_fast",
		"a.b.c":         "luaopen_a_b_c",
		"my-app.fast":   "luaopen_app_fast",
		"v1-my-app.x.y": "luaopen_my-app_x_y",
		"-x":            "luaopen_x",
	} {
		assert.Equal(t, want, luaEntry(name), name)
	}

	assert.Equal(t, "fast", requireName("", "fast"))
	assert.Equal(t, "demo.fast", requireName("demo", "fast"))
	assert.Equal(t, "demo.a.b", requireName("demo", "a.b"))
}

// TestCxxLuaAPI pins which undefined symbols are the Lua API by C++ names.
func TestCxxLuaAPI(t *testing.T) {
	t.Parallel()

	for _, symbol := range []string{
		"_Z15luaL_checkstackP9lua_StateiPKc",
		"_Z10lua_settopP9lua_Statei",
		"_Z18luaL_checkintegerP9lua_Statei",
		"_Z12luaopen_baseP9lua_State",
		"_Z14luaJIT_setmodeP9lua_Stateii",
		"_Z21luaM_metrics_snapshotP9lua_StateP12luam_Metrics",
	} {
		assert.True(t, cxxLuaAPI(symbol), symbol)
	}

	for _, symbol := range []string{
		"luaL_checkstack",
		"lua_settop",
		"_Z3fooP9lua_State",
		"_ZN2ns8lua_callEv",
		"_ZNSt6vectorIiSaIiEE9push_backERKi",
		"_Z",
		"_Z0",
		"_Z99lua_",
		"_Zx",
		"__cxa_throw",
		"_Z4luaXv",
	} {
		assert.False(t, cxxLuaAPI(symbol), symbol)
	}
}

// TestCheckLuaModule checks what the check of a lua-c module reports.
func TestCheckLuaModule(t *testing.T) {
	t.Parallel()

	good := moduleSymbols{
		arch:      "",
		defined:   []string{"luaopen_demo_fast", "helper"},
		undefined: []string{"lua_pushinteger", "luaL_checklstring", "_ZdlPv"},
	}
	require.NoError(t, checkLuaModule("/lib/fast.so", "demo.fast", []moduleSymbols{good}))

	err := checkLuaModule("/lib/fast.so", "fast", []moduleSymbols{good})
	require.ErrorIs(t, err, errNoEntry)
	assert.Equal(t, `/lib/fast.so: the module exports no luaopen_fast, the function `+
		`require("fast") calls: define int luaopen_fast(lua_State *L), extern "C" in C++, `+
		`or set module to the name the module is for`, err.Error())

	cxx := moduleSymbols{
		arch:    "",
		defined: []string{"luaopen_demo_fast"},
		undefined: []string{
			"_Z15luaL_checkstackP9lua_StateiPKc", "_Z10lua_settopP9lua_Statei",
			"_Z15luaL_checkstackP9lua_StateiPKc", "_Z13lua_pushvalueP9lua_Statei",
			"_Z15lua_pushintegerP9lua_Statel", "_ZdlPv",
		},
	}

	err = checkLuaModule("/lib/fast.so", "demo.fast", []moduleSymbols{cxx})
	require.ErrorIs(t, err, errCxxLuaAPI)
	require.NotErrorIs(t, err, errNoEntry)
	assert.Equal(t, `/lib/fast.so: the module needs functions of the Lua API by their C++ `+
		`names: _Z15luaL_checkstackP9lua_StateiPKc, _Z10lua_settopP9lua_Statei, `+
		`_Z13lua_pushvalueP9lua_Statei and 1 more: lua.h, lauxlib.h and lualib.h of `+
		`Tarantool declare no extern "C", so a C++ source includes them through <lua.hpp>, `+
		`which does, or within extern "C" { }`, err.Error())

	// Both at once.
	cxx.defined = nil

	err = checkLuaModule("/lib/fast.so", "demo.fast", []moduleSymbols{cxx})
	require.ErrorIs(t, err, errCxxLuaAPI)
	require.ErrorIs(t, err, errNoEntry)

	// Each slice of a universal file, the one that fails named.
	arm := good

	arm.arch = "ArmArch64"

	x86 := good

	x86.arch = "CpuAmd64"
	x86.defined = nil

	err = checkLuaModule("/lib/fast.dylib", "demo.fast", []moduleSymbols{arm, x86})
	require.ErrorIs(t, err, errNoEntry)
	assert.Contains(t, err.Error(), "/lib/fast.dylib (CpuAmd64): ")
	require.NoError(t, checkLuaModule("/lib/fast.dylib", "demo.fast", []moduleSymbols{arm, arm}))
}

// TestReadSymbolsOfNoModule checks that a file of no format a module has is
// told from one that cannot be read.
func TestReadSymbolsOfNoModule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for name, body := range map[string]string{
		"empty.so": "", "text.so": "not a module\n", "short.so": "\x7fEL",
	} {
		path := filepath.Join(dir, name)

		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

		_, err := readSymbols(path)
		require.ErrorIs(t, err, errUnknownFormat, name)
	}

	_, err := readSymbols(filepath.Join(dir, "missing.so"))
	require.Error(t, err)
	require.NotErrorIs(t, err, errUnknownFormat)

	// The test binary is a native file of the host.
	self, err := os.Executable()
	require.NoError(t, err)

	symbols, err := readSymbols(self)
	require.NoError(t, err)
	assert.Len(t, symbols, 1)
}
