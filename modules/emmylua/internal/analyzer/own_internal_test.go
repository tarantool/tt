package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRock is a rock installed into a test tree: its modules, by file under
// share/tarantool.
type testRock struct {
	name  string
	files []string
}

// writeTree installs rocks into the rock tree of project as tt package
// installs them: the files under .rocks/share/tarantool, the tree manifest
// and the rock_manifest of each rock, at version 1.0.0-1.
func writeTree(t *testing.T, project string, rocks ...testRock) {
	t.Helper()

	const version = "1.0.0-1"

	deploy := filepath.Join(project, ".rocks", "share", "tarantool")

	var repository strings.Builder

	for _, rock := range rocks {
		var modules strings.Builder

		lua := luaDir{}

		for _, file := range rock.files {
			path := filepath.Join(deploy, filepath.FromSlash(file))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte("return {}\n"), 0o644))

			module := strings.ReplaceAll(strings.TrimSuffix(file, ".lua"), "/", ".")
			fmt.Fprintf(&modules, "[%q] = %q,\n", module, file)
			lua.add(strings.Split(file, "/"))
		}

		installDir := filepath.Join(deploy, "rocks", rock.name, version)
		require.NoError(t, os.MkdirAll(installDir, 0o755))

		rockManifest := "rock_manifest = {\n" +
			fmt.Sprintf("[%q] = %q,\n", rock.name+"-"+version+".rockspec", testMD5) +
			"lua = " + lua.render() + "\n}\n"
		require.NoError(t, os.WriteFile(filepath.Join(installDir, "rock_manifest"),
			[]byte(rockManifest), 0o644))

		fmt.Fprintf(&repository, "[%q] = {[%q] = {{arch = \"installed\", commands = {}, "+
			"dependencies = {}, modules = {\n%s}}}},\n", rock.name, version, modules.String())
	}

	manifest := "commands = {}\ndependencies = {}\nmodules = {}\nrepository = {\n" +
		repository.String() + "}\n"
	require.NoError(t, os.WriteFile(filepath.Join(deploy, "rocks", "manifest"),
		[]byte(manifest), 0o644))
}

// testMD5 is the md5 the test trees record for every file.
const testMD5 = "00000000000000000000000000000000"

// luaDir is a section of a rock_manifest as LuaRocks nests it: a file is its
// md5, a directory a table of its entries.
type luaDir map[string]luaDir

// add adds the file of the path parts.
func (d luaDir) add(parts []string) {
	if len(parts) == 1 {
		d[parts[0]] = nil

		return
	}

	sub, ok := d[parts[0]]
	if !ok || sub == nil {
		sub = luaDir{}
		d[parts[0]] = sub
	}

	sub.add(parts[1:])
}

// render writes d as a Lua table.
func (d luaDir) render() string {
	var out strings.Builder

	out.WriteString("{\n")

	for name, sub := range d {
		if sub == nil {
			fmt.Fprintf(&out, "[%q] = %q,\n", name, testMD5)
		} else {
			fmt.Fprintf(&out, "[%q] = %s,\n", name, sub.render())
		}
	}

	out.WriteString("}")

	return out.String()
}

func TestOwnModules(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	deploy := filepath.Join(project, ".rocks", "share", "tarantool")

	require.NoError(t, os.WriteFile(filepath.Join(project, "app.manifest.toml"), []byte(
		"[package]\nname = \"kv\"\n\n[products.cli]\nrock = \"kv-cli\"\n"), 0o644))

	writeTree(t, project,
		testRock{name: "kv", files: []string{"kv/init.lua", "kv/version.lua", "flat.lua"}},
		testRock{name: "kv-cli", files: []string{"kv_cli/init.lua"}},
		testRock{name: "checks", files: []string{"checks.lua"}},
		testRock{name: "metrics", files: []string{"metrics/init.lua"}},
	)

	own, err := OwnModules(t.Context(), project)
	require.NoError(t, err)

	// A directory of its own is left out whole, a file at the top alone;
	// the modules of the other rocks stay.
	assert.Equal(t, []string{
		filepath.Join(deploy, "flat.lua"),
		filepath.Join(deploy, "kv"),
		filepath.Join(deploy, "kv_cli"),
	}, own)
}

func TestOwnModulesShared(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	deploy := filepath.Join(project, ".rocks", "share", "tarantool")

	require.NoError(t, os.WriteFile(filepath.Join(project, "app.manifest.toml"),
		[]byte("[package]\nname = \"kv\"\n"), 0o644))

	// Another rock installs a module into the namespace of the package.
	writeTree(t, project,
		testRock{name: "kv", files: []string{"kv/init.lua", "kv/store/space.lua"}},
		testRock{name: "kv-ext", files: []string{"kv/ext.lua"}},
	)

	own, err := OwnModules(t.Context(), project)
	require.NoError(t, err)

	assert.Equal(t, []string{
		filepath.Join(deploy, "kv", "init.lua"),
		filepath.Join(deploy, "kv", "store", "space.lua"),
	}, own)
}

func TestOwnModulesNone(t *testing.T) {
	t.Parallel()

	// No manifest, and no rocks.
	project := t.TempDir()

	own, err := OwnModules(t.Context(), project)
	require.NoError(t, err)
	assert.Empty(t, own)

	require.NoError(t, os.WriteFile(filepath.Join(project, "app.manifest.toml"),
		[]byte("[package]\nname = \"kv\"\n"), 0o644))

	own, err = OwnModules(t.Context(), project)
	require.NoError(t, err)
	assert.Empty(t, own)

	// Rocks, but not the package's: it was never built.
	writeTree(t, project, testRock{name: "checks", files: []string{"checks.lua"}})

	own, err = OwnModules(t.Context(), project)
	require.NoError(t, err)
	assert.Empty(t, own)
}

func TestLayoutConfigOwn(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	own := filepath.Join(project, ".rocks", "share", "tarantool", "kv")

	config := Layout{ProjectDir: project, Own: []string{own}}.Config()
	workspace, ok := config["workspace"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{own}, workspace["ignoreDir"])

	// No package of its own: no ignoreDir at all.
	config = Layout{ProjectDir: project}.Config()
	workspace, ok = config["workspace"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, workspace, "ignoreDir")
}
