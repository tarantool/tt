package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFormatConfig writes content to the file name under dir, making the
// directories it is in.
func writeFormatConfig(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	return path
}

func TestFormatLevelConfigured(t *testing.T) {
	t.Parallel()

	const withLevel, withoutLevel = "[syntax]\nlevel = \"Lua54\"\n", "[indent]\nwidth = 2\n"

	project := t.TempDir()

	// No configuration: luafmt's default, which tt replaces.
	assert.False(t, FormatLevelConfigured(project, []string{project}, ""))

	writeFormatConfig(t, project, ".luafmt.toml", withoutLevel)
	writeFormatConfig(t, project, "luafmt.toml", withLevel)

	// .luafmt.toml comes first in a directory, and luafmt reads one.
	assert.False(t, FormatLevelConfigured(project, []string{project}, ""))

	// The nearest configuration of a path is luafmt's: in the directory, in
	// that of a file, above them; a relative path is taken from dir.
	writeFormatConfig(t, project, "app/luafmt.toml", withLevel)
	writeFormatConfig(t, project, "app/lib/init.lua", "")

	for _, paths := range [][]string{
		{"app"},
		{filepath.Join(project, "app")},
		{"app/lib/init.lua"},
		{"app/lib"},
		{"app/missing.lua"},
		{"spec", "app"},
	} {
		assert.True(t, FormatLevelConfigured(project, paths, ""), paths)
	}

	assert.False(t, FormatLevelConfigured(project, []string{"spec", "init.lua"}, ""))

	// A level set in another form of TOML counts.
	for _, content := range []string{
		"syntax.level = \"LuaJIT\"\n",
		"syntax = {level = \"LuaJIT\"}\n",
	} {
		writeFormatConfig(t, project, ".luafmt.toml", content)
		assert.True(t, FormatLevelConfigured(project, []string{project}, ""), content)
	}

	// Not a key of luafmt: the names are matched as luafmt matches them.
	writeFormatConfig(t, project, ".luafmt.toml", "[Syntax]\nLevel = \"LuaJIT\"\n")
	assert.False(t, FormatLevelConfigured(project, []string{project}, ""))

	// A configuration tt cannot read overrides nothing.
	writeFormatConfig(t, project, ".luafmt.toml", "[syntax\n")
	assert.True(t, FormatLevelConfigured(project, []string{project}, ""))

	// A path without a configuration takes that of the directory luafmt runs
	// in.
	writeFormatConfig(t, project, ".luafmt.toml", withLevel)
	assert.True(t, FormatLevelConfigured(project, []string{t.TempDir()}, ""))
	assert.False(t, FormatLevelConfigured(t.TempDir(), []string{t.TempDir()}, ""))
}

func TestFormatLevelConfiguredExplicit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// The project's configuration sets the level; --config replaces it.
	writeFormatConfig(t, dir, ".luafmt.toml", "[syntax]\nlevel = \"Lua54\"\n")

	for _, testCase := range []struct {
		name, content string
		want          bool
	}{
		{name: "style.toml", content: "[indent]\nwidth = 2\n", want: false},
		{name: "style.toml", content: "[syntax]\nlevel = \"Lua51\"\n", want: true},
		{name: "style.json", content: `{"indent": {"width": 2}}`, want: false},
		{name: "style.json", content: `{"syntax": {"level": "Lua51"}}`, want: true},
		// Another extension is TOML, then JSON, as luafmt reads it.
		{name: "style", content: "[indent]\nwidth = 2\n", want: false},
		{name: "style", content: `{"syntax": {"level": "Lua51"}}`, want: true},
		{name: "style.conf", content: `{"indent": {"width": 2}}`, want: false},
		// tt reads no YAML, and a file that does not parse is luafmt's to
		// report: neither is overridden.
		{name: "style.yaml", content: "indent:\n  width: 2\n", want: true},
		{name: "style.json", content: `{"indent":`, want: true},
	} {
		path := writeFormatConfig(t, dir, testCase.name, testCase.content)
		assert.Equal(t, testCase.want, FormatLevelConfigured(dir, []string{dir}, path),
			testCase.name, testCase.content)
		assert.Equal(t, testCase.want,
			FormatLevelConfigured(dir, []string{dir}, testCase.name), "relative to dir")
	}

	assert.True(t, FormatLevelConfigured(dir, []string{dir}, "missing.toml"))
}
