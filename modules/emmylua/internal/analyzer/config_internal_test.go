package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLayoutConfig(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	stubs := t.TempDir()
	rockModules := filepath.Join(project, ".rocks", "share", "tarantool")
	require.NoError(t, os.MkdirAll(rockModules, 0o755))

	layout := Layout{ProjectDir: project, Stubs: stubs}

	// .rocks/share/lua/5.1 does not exist, so it is not a library.
	assert.Equal(t, []string{stubs, rockModules}, layout.Libraries())

	path, err := layout.WriteConfig()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(project, ".tarantool", "emmylua", "emmyrc.json"), path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var config struct {
		Runtime struct {
			Version string `json:"version"`
		} `json:"runtime"`
		Workspace struct {
			Library     []string `json:"library"`
			IgnoreGlobs []string `json:"ignoreGlobs"` //nolint:tagliatelle // EmmyLua's name.
		} `json:"workspace"`
		Diagnostics struct {
			Globals []string `json:"globals"`
		} `json:"diagnostics"`
	}

	require.NoError(t, json.Unmarshal(data, &config))
	assert.Equal(t, "LuaJIT", config.Runtime.Version)
	assert.Equal(t, []string{stubs, rockModules}, config.Workspace.Library)
	assert.Equal(t, []string{".rocks/**/*.lua", ".tarantool/**/*.lua"},
		config.Workspace.IgnoreGlobs)
	assert.Contains(t, config.Diagnostics.Globals, "box")

	// Without stubs, the libraries are the rocks'.
	assert.Equal(t, []string{rockModules}, Layout{ProjectDir: project}.Libraries())
}

func TestLayoutConfigFiles(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	layout := Layout{ProjectDir: project}

	assert.Equal(t, []string{"gen"}, layout.ConfigFiles("gen"))

	require.NoError(t, os.WriteFile(filepath.Join(project, ".emmyrc.json"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(project, ".luarc.json"), []byte("{}"), 0o644))

	assert.Equal(t, []string{
		"gen", filepath.Join(project, ".luarc.json"), filepath.Join(project, ".emmyrc.json"),
	}, layout.ConfigFiles("gen"))
}

func TestFindStubs(t *testing.T) {
	t.Parallel()

	// A package: <root>/bin/tarantool and <root>/share/tarantool/emmylua.
	// The temporary directory is resolved, as FindStubs resolves a link: on
	// macOS it is under /var, a link to /private/var.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	stubs := filepath.Join(root, "share", "tarantool", "emmylua")
	require.NoError(t, os.MkdirAll(stubs, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))

	executable := filepath.Join(root, "bin", "tarantool")
	require.NoError(t, os.WriteFile(executable, []byte(""), 0o755))

	assert.Equal(t, stubs, FindStubs(executable))

	// A link to it elsewhere, as tt binary activates one, is followed.
	links := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.MkdirAll(links, 0o755))

	link := filepath.Join(links, "tarantool")
	require.NoError(t, os.Symlink(executable, link))

	assert.Equal(t, stubs, FindStubs(link))

	// The stubs next to the link come before those of its target.
	linkStubs := filepath.Join(filepath.Dir(links), "share", "tarantool", "emmylua")
	require.NoError(t, os.MkdirAll(linkStubs, 0o755))

	assert.Equal(t, linkStubs, FindStubs(link))

	assert.Empty(t, FindStubs(filepath.Join(t.TempDir(), "bin", "tarantool")))
	assert.Empty(t, FindStubs(""))
}

func TestProgramPath(t *testing.T) {
	t.Parallel()

	project := t.TempDir()

	_, err := ProgramPath(project, Check)
	require.ErrorIs(t, err, ErrNotPrepared)
	assert.Contains(t, err.Error(), "tt emmylua prepare")

	bin := filepath.Join(project, ".rocks", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "emmylua_check"), []byte(""), 0o755))

	path, err := ProgramPath(project, Check)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(bin, "emmylua_check"), path)
}
