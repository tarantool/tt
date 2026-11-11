//go:build integration

package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// e2eManifest is my-app with a real lua-c native component: fast_hash.c is
// compiled by the cc driver against the Tarantool headers.
const e2eManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.lua]
path = '.'
include = ['*.lua']

[components.native]
path = 'native/'

[components.native.build]
backend = 'lua-c'
module = 'fast_hash'
sources = ['fast_hash.c']

[products.default]
components = ['lua', 'native']
default = true
`

// fastHashC is a minimal Lua C extension exporting luaopen_fast_hash.
const fastHashC = `#include <lua.h>

static int l_hash(lua_State *L) {
	lua_pushinteger(L, 42);
	return 1;
}

int luaopen_fast_hash(lua_State *L) {
	lua_pushcfunction(L, l_hash);
	return 1;
}
`

// tarantoolInfoForTest derives the Tarantool facts from a live binary, skipping
// the test when tarantool or its development headers are unavailable. The
// prefix is TT_CLI_TARANTOOL_PREFIX, when it is set, as tt takes it, or the
// one of the build banner.
func tarantoolInfoForTest(t *testing.T) rocks.TarantoolInfo {
	t.Helper()

	exe, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool not found in PATH")
	}

	out, err := exec.CommandContext(t.Context(), exe, "--version").Output()
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.GreaterOrEqual(t, len(lines), 3, "unexpected tarantool --version output")

	version := lines[0][strings.LastIndex(lines[0], " ")+1:]

	prefix := ""
	prefixRe := regexp.MustCompile(`-DCMAKE_INSTALL_PREFIX=(\S+)`)

	if m := prefixRe.FindStringSubmatch(lines[2]); m != nil {
		prefix = m[1]
	}

	if env := os.Getenv("TT_CLI_TARANTOOL_PREFIX"); env != "" {
		prefix = env
	}

	info := rocks.TarantoolInfo{Executable: exe, Prefix: prefix, Version: version}

	_, err = os.Stat(filepath.Join(info.IncludeDir(), "lua.h"))
	if err != nil {
		t.Skipf("Tarantool headers not found under %s", info.IncludeDir())
	}

	return info
}

func TestRunE2E_compilesNativeComponent(t *testing.T) {
	info := tarantoolInfoForTest(t)

	dir := setupProject(t, e2eManifest, map[string]string{
		"init.lua":           "-- init",
		"native/fast_hash.c": fastHashC,
	})

	opts := dryOptions(dir)

	opts.Tarantool = info
	require.NoError(t, Run(context.Background(), opts))

	tree := filepath.Join(dir, ".rocks")

	// The Lua C extension is a .so on Linux and a .dylib on macOS, as
	// backend.moduleExt names it.
	module := "fast_hash.so"
	if runtime.GOOS == "darwin" {
		module = "fast_hash.dylib"
	}

	so := filepath.Join(tree, "lib/tarantool/my-app", module)

	fi, err := os.Stat(so)
	require.NoError(t, err, "compiled artifact must exist")
	assert.Positive(t, fi.Size(), "compiled artifact must be non-empty")

	assert.FileExists(t, filepath.Join(tree, "share/tarantool/my-app/init.lua"))
	assert.FileExists(t, filepath.Join(tree, "share/tarantool/my-app/version.lua"))
}

// e2eCMakeManifest is my-app with fast_hash built by cmake.
const e2eCMakeManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.native]
path = 'native/'

[components.native.build]
backend = 'cmake'
output = ['fast_hash.so']
`

// fastHashCMakeLists builds fast_hash.c against the headers of Tarantool,
// found as FindTarantool.cmake of the Tarantool modules finds them.
const fastHashCMakeLists = `cmake_minimum_required(VERSION 3.15)
project(fast_hash C)

find_path(TARANTOOL_INCLUDE_DIR tarantool/module.h)
if(NOT TARANTOOL_INCLUDE_DIR)
    message(FATAL_ERROR "tarantool/module.h not found")
endif()

add_library(fast_hash MODULE fast_hash.c)
target_include_directories(fast_hash PRIVATE ${TARANTOOL_INCLUDE_DIR}/tarantool)
set_target_properties(fast_hash PROPERTIES PREFIX "" SUFFIX ".so")
if(APPLE)
    target_link_options(fast_hash PRIVATE -undefined dynamic_lookup)
endif()
`

// TestRunE2E_cmakeComponent builds a module with cmake against the headers of
// the Tarantool in PATH, and loads it from the tree in that Tarantool.
func TestRunE2E_cmakeComponent(t *testing.T) {
	info := tarantoolInfoForTest(t)

	_, err := exec.LookPath("cmake")
	if err != nil {
		t.Skip("cmake not found in PATH")
	}

	dir := setupProject(t, e2eCMakeManifest, map[string]string{
		"native/CMakeLists.txt": fastHashCMakeLists,
		"native/fast_hash.c":    fastHashC,
		// Lua takes the name of luaopen_ from after the dash of my-app.
		"check.lua": "package.cpath = arg[1] .. '/?.so;' .. arg[1] .. '/?.dylib;' .. " +
			"package.cpath\nprint(require('fast_hash')())\nos.exit(0)\n",
	})

	opts := dryOptions(dir)

	opts.Tarantool = info
	require.NoError(t, Run(context.Background(), opts))

	lib := filepath.Join(dir, ".rocks", "lib", "tarantool", "my-app")
	printed, err := exec.CommandContext(t.Context(), info.Executable,
		filepath.Join(dir, "check.lua"), lib).CombinedOutput()
	require.NoError(t, err, string(printed))
	assert.Equal(t, "42", strings.TrimSpace(string(printed)))
}
