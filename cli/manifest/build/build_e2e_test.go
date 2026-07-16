//go:build integration

package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/rocks"
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
// the test when tarantool or its development headers are unavailable.
func tarantoolInfoForTest(t *testing.T) rocks.TarantoolInfo {
	t.Helper()

	exe, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool not found in PATH")
	}

	out, err := exec.Command(exe, "--version").Output()
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.GreaterOrEqual(t, len(lines), 3, "unexpected tarantool --version output")

	version := lines[0][strings.LastIndex(lines[0], " ")+1:]

	prefix := ""
	prefixRe := regexp.MustCompile(`-DCMAKE_INSTALL_PREFIX=(\S+)`)
	if m := prefixRe.FindStringSubmatch(lines[2]); m != nil {
		prefix = m[1]
	}

	info := rocks.TarantoolInfo{Executable: exe, Prefix: prefix, Version: version}
	if _, err := os.Stat(filepath.Join(info.IncludeDir(), "lua.h")); err != nil {
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
	so := filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so")

	fi, err := os.Stat(so)
	require.NoError(t, err, "compiled artifact must exist")
	assert.Positive(t, fi.Size(), "compiled artifact must be non-empty")

	assert.FileExists(t, filepath.Join(tree, "share/tarantool/my-app/init.lua"))
	assert.FileExists(t, filepath.Join(tree, "share/tarantool/my-app/version.lua"))
}
