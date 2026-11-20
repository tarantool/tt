//go:build integration

package cmake

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build/v3/buildtest"
)

// minTarantoolMajor is the lowest Tarantool major version tt v3 supports.
const minTarantoolMajor = 3

// hostTarantool is the Tarantool in PATH the integration tests build against.
type hostTarantool struct {
	// executable is the tarantool binary.
	executable string
	// prefix is its install prefix.
	prefix string
	// includeDir is the directory of its module.h, as tt tells a build.
	includeDir string
}

// requireHostTarantool finds a Tarantool of a supported version in PATH,
// with its C headers under its prefix, <prefix>/include/tarantool, as tt
// finds them, and skips the test when there is none.
func requireHostTarantool(t *testing.T) hostTarantool {
	t.Helper()

	executable, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skipf("tarantool not found in PATH: %v", err)
	}

	out, err := exec.CommandContext(t.Context(), executable, "--version").Output()
	if err != nil {
		t.Skipf("cannot run %s --version: %v", executable, err)
	}

	line, _, _ := bytes.Cut(out, []byte("\n"))
	fields := strings.Fields(string(line))

	if len(fields) == 0 {
		t.Skipf("cannot read the version of %s from %q", executable, line)
	}

	ver := fields[len(fields)-1]
	major, _, _ := strings.Cut(ver, ".")

	number, err := strconv.Atoi(major)
	if err != nil || number < minTarantoolMajor {
		t.Skipf("tarantool %s is not supported: need >= %d.0", ver, minTarantoolMajor)
	}

	// The prefix is that of the binary as found and with its links resolved,
	// for a layout like Homebrew's bin -> Cellar.
	candidates := []string{executable}

	resolved, err := filepath.EvalSymlinks(executable)
	if err == nil && resolved != executable {
		candidates = append(candidates, resolved)
	}

	for _, bin := range candidates {
		prefix := filepath.Dir(filepath.Dir(bin))
		includeDir := filepath.Join(prefix, "include", "tarantool")

		_, err := os.Stat(filepath.Join(includeDir, "module.h"))
		if err == nil {
			return hostTarantool{executable: executable, prefix: prefix, includeDir: includeDir}
		}
	}

	t.Skipf("tarantool C module headers (module.h) not found for %s", executable)

	return hostTarantool{}
}

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

// checkLua loads the module of the directory arg[1] and prints its answer.
const checkLua = "package.cpath = arg[1] .. '/?.so;' .. arg[1] .. '/?.dylib;' .. " +
	"package.cpath\nprint(require('answer').answer())\nos.exit(0)\n"

// TestCompilesAgainstTarantoolHeaders builds a Lua C module with cmake
// against the real headers of the Tarantool in PATH, found through the
// CMAKE_PREFIX_PATH the backend passes, and loads it in that Tarantool.
func TestCompilesAgainstTarantoolHeaders(t *testing.T) {
	tarantool := requireHostTarantool(t)

	_, err := exec.LookPath("cmake")
	if err != nil {
		t.Skip("cmake not found in PATH")
	}

	answer := module()
	harness := buildtest.New(t, Backend(), answer)
	facts := harness.Context

	facts.Tarantool.Prefix = tarantool.prefix
	facts.Tarantool.IncludeDir = tarantool.includeDir

	for name, body := range map[string]string{
		"CMakeLists.txt": luaModuleCMakeLists,
		"answer.c":       luaModuleC,
		"check.lua":      checkLua,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(facts.Dirs.Cwd, name), []byte(body), 0o600))
	}

	bld, err := buildtest.Validate(Backend(), answer, block([]string{"answer.so"}, nil))
	require.NoError(t, err)

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.FileExists(t, filepath.Join(facts.Dirs.Modules, "answer"+facts.Target.ModuleExt()))

	printed, err := exec.CommandContext(t.Context(), tarantool.executable,
		filepath.Join(facts.Dirs.Cwd, "check.lua"), facts.Dirs.Modules).CombinedOutput()
	require.NoError(t, err, string(printed))
	assert.Equal(t, "42", strings.TrimSpace(string(printed)))
}
