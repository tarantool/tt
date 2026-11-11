//go:build integration

package backend

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// luaModuleCMakeLists builds the Lua C module answer against the headers of
// Tarantool, found as FindTarantool.cmake of the Tarantool modules finds
// them: find_path of tarantool/module.h, which searches <prefix>/include of
// CMAKE_PREFIX_PATH. The module takes the Lua C API from tarantool when it is
// loaded.
const luaModuleCMakeLists = `cmake_minimum_required(VERSION 3.15)
project(answer C)

find_path(TARANTOOL_INCLUDE_DIR tarantool/module.h)
if(NOT TARANTOOL_INCLUDE_DIR)
    message(FATAL_ERROR "tarantool/module.h not found")
endif()

add_library(answer MODULE answer.c)
target_include_directories(answer PRIVATE ${TARANTOOL_INCLUDE_DIR}/tarantool)
set_target_properties(answer PROPERTIES PREFIX "" SUFFIX ".so")
if(APPLE)
    target_link_options(answer PRIVATE -undefined dynamic_lookup)
endif()
`

// luaModuleC is a Lua C module of one function, answer, which returns 42.
const luaModuleC = `#include <module.h>
#include <lua.h>
#include <lauxlib.h>

static int answer(lua_State *L) {
	lua_pushinteger(L, 42);
	return 1;
}

LUA_API int luaopen_answer(lua_State *L) {
	lua_newtable(L);
	lua_pushcfunction(L, answer);
	lua_setfield(L, -2, "answer");
	return 1;
}
`

// TestCMakeCompilesAgainstTarantoolHeaders builds a Lua C module with cmake
// against the real headers of the Tarantool in PATH, found through the
// CMAKE_PREFIX_PATH the backend passes, and loads it in that Tarantool.
func TestCMakeCompilesAgainstTarantoolHeaders(t *testing.T) {
	toolchain := requireHostToolchain(t)

	_, err := exec.LookPath(cmakeTool)
	if err != nil {
		t.Skip("cmake not found in PATH")
	}

	prefix := filepath.Dir(filepath.Dir(toolchain.IncludeDir))

	source := t.TempDir()
	writeFiles(t, source, map[string]string{
		"CMakeLists.txt": luaModuleCMakeLists,
		"answer.c":       luaModuleC,
		"check.lua": "package.cpath = arg[1] .. '/?.so;' .. arg[1] .. '/?.dylib;' .. " +
			"package.cpath\nprint(require('answer').answer())\nos.exit(0)\n",
	})

	out := t.TempDir()
	build := manifest.Build{Backend: BackendCMake, Output: []string{"answer.so"}}
	env := Env{
		OutputDir:           out,
		Module:              true,
		TarantoolPrefix:     prefix,
		TarantoolIncludeDir: toolchain.IncludeDir,
		BuildDir:            filepath.Join(t.TempDir(), "answer"),
	}

	require.NoError(t, cmakeBackend{}.Run(context.Background(), build, source, env))
	assert.FileExists(t, filepath.Join(out, "answer"+moduleExt(runtime.GOOS, linuxModuleExt)))

	printed, err := exec.CommandContext(context.Background(), toolchain.Tarantool,
		filepath.Join(source, "check.lua"), out).CombinedOutput()
	require.NoError(t, err, string(printed))
	assert.Equal(t, "42", strings.TrimSpace(string(printed)))
}
