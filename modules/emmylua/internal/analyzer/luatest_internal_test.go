package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeLuatest installs a luatest into the library dir: its entry module
// and its assertions.
func writeLuatest(t *testing.T, dir string) string {
	t.Helper()

	init := filepath.Join(dir, "luatest", "init.lua")
	require.NoError(t, os.MkdirAll(filepath.Dir(init), 0o755))
	require.NoError(t, os.WriteFile(init, []byte("return {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "luatest", "assertions.lua"),
		[]byte("return {}\n"), 0o644))

	return init
}

// TestLayoutConfigLuatest checks that a library holding luatest has the
// configuration read the stub tt writes for its entry module, first among
// the libraries, and leave the entry module of the rock out, while one
// without luatest has neither; and that the project's own luatest is left
// as it is.
func TestLayoutConfigLuatest(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	rocks := filepath.Join(project, ".rocks", "share", "tarantool")
	require.NoError(t, os.MkdirAll(rocks, 0o755))

	layout := Layout{ProjectDir: project}
	stubs := filepath.Join(project, ".tarantool", "emmylua", "stubs")

	workspace := func(layout Layout) map[string]any {
		t.Helper()

		workspace, ok := layout.Config()["workspace"].(map[string]any)
		require.True(t, ok)

		return workspace
	}

	assert.Equal(t, []string{rocks}, workspace(layout)["library"])

	path, err := layout.WriteConfig()
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(filepath.Dir(path), "stubs"))

	init := writeLuatest(t, rocks)

	assert.Equal(t, []string{stubs, rocks}, workspace(layout)["library"])
	assert.Contains(t, workspace(layout)["ignoreDir"], init)

	_, err = layout.WriteConfig()
	require.NoError(t, err)

	stub, err := os.ReadFile(filepath.Join(stubs, "luatest", "init.lua"))
	require.NoError(t, err)
	assert.Contains(t, string(stub), "local luatest = require('luatest.assertions')")
	assert.Contains(t, string(stub), "function luatest.before_suite(fn) end")

	// A luatest of --library, a checkout of the rock say, outside a
	// project: the stub goes beside the configuration in the cache.
	checkout := t.TempDir()
	extraInit := writeLuatest(t, checkout)
	outside := Layout{
		ProjectDir: t.TempDir(), Extra: []string{checkout},
		Path: filepath.Join(t.TempDir(), "config", "emmyrc.json"),
	}

	assert.Equal(t, []string{filepath.Join(filepath.Dir(outside.Path), "stubs"), checkout},
		workspace(outside)["library"])
	assert.Contains(t, workspace(outside)["ignoreDir"], extraInit)

	_, err = outside.WriteConfig()
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(filepath.Dir(outside.Path), "stubs", "luatest",
		"init.lua"))

	// The project's own package is luatest: its code is left as it is.
	own := Layout{ProjectDir: project, Own: []string{filepath.Join(rocks, "luatest")}}
	assert.Equal(t, []string{rocks}, workspace(own)["library"])
	assert.NotContains(t, workspace(own)["ignoreDir"], init)
}
