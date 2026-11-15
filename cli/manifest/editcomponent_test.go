package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// wasmComponentBlock is the component tt wasm new declares.
func wasmComponentBlock() manifest.ComponentBlock {
	return manifest.ComponentBlock{
		Name:    "router",
		Comment: "A WebAssembly component, made by tt wasm new and built by tarawasm.",
		Path:    "router",
		Include: []string{"dist/router.wasm"},
		Build: []manifest.BlockField{
			{Key: "backend", Value: "shell"},
			{Key: "command", Value: "tarawasm"},
			{Key: "args", Value: []string{"all"}},
			{Key: "incremental", Value: true},
		},
	}
}

// TestAppendComponent checks that a component is appended as its own tables
// after a blank line, every line of the source kept, in the form tt wasm new
// wrote by hand, and that the manifest it makes parses and validates with
// the component declared.
func TestAppendComponent(t *testing.T) {
	t.Parallel()

	src := editHeader + "# The end of the manifest.\n"

	out, err := manifest.AppendComponent([]byte(src), wasmComponentBlock())
	require.NoError(t, err)
	assert.Equal(t, src+`
# A WebAssembly component, made by tt wasm new and built by tarawasm.
[components.router]
path = "router"
include = ["dist/router.wasm"]

[components.router.build]
backend = "shell"
command = "tarawasm"
args = ["all"]
incremental = true
`, string(out))

	mfst := reparse(t, out)
	require.Contains(t, mfst.Components, "router")
	assert.Equal(t, "router", mfst.Components["router"].Path)
	assert.Equal(t, []string{"dist/router.wasm"}, mfst.Components["router"].Include)
	assert.Equal(t, "shell", mfst.Components["router"].Build.Backend)
	assert.True(t, mfst.Components["router"].Build.IncrementalValue())
}

// TestAppendComponentForms checks a source with no final line break and CRLF
// line endings, a component of no comment, include and build, the quoting of
// keys and strings, a comment of several lines and the other values.
func TestAppendComponentForms(t *testing.T) {
	t.Parallel()

	out, err := manifest.AppendComponent([]byte(strings.TrimSuffix(editHeader, "\n")),
		manifest.ComponentBlock{Name: "lua", Path: "src", Comment: "", Include: nil, Build: nil})
	require.NoError(t, err)
	assert.Equal(t, editHeader+"\n[components.lua]\npath = \"src\"\n", string(out))
	reparse(t, out)

	crlf := strings.ReplaceAll(editHeader, "\n", "\r\n")

	out, err = manifest.AppendComponent([]byte(crlf), manifest.ComponentBlock{
		Name: "a.b", Path: `dir "x"\y`, Comment: "one\n\ntwo",
		Include: []string{"*.lua", "ü.lua"},
		Build: []manifest.BlockField{
			{Key: "backend", Value: "make"}, {Key: "make_target", Value: "all"},
			{Key: "jobs", Value: 4},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, crlf+"\r\n# one\r\n#\r\n# two\r\n"+
		`[components."a.b"]`+"\r\n"+`path = "dir \"x\"\\y"`+"\r\n"+
		`include = ["*.lua", "ü.lua"]`+"\r\n\r\n"+
		`[components."a.b".build]`+"\r\n"+`backend = "make"`+"\r\n"+
		`make_target = "all"`+"\r\n"+"jobs = 4\r\n", string(out))

	mfst, _, err := manifest.ParseManifest(out)
	require.NoError(t, err)
	assert.Equal(t, `dir "x"\y`, mfst.Components["a.b"].Path)
	assert.Equal(t, map[string]any{"make_target": "all", "jobs": int64(4)},
		mfst.Components["a.b"].Build.Rest)
}

// TestAppendComponentRefuses checks the blocks AppendComponent cannot write.
func TestAppendComponentRefuses(t *testing.T) {
	t.Parallel()

	for name, block := range map[string]manifest.ComponentBlock{
		"no name": {Name: "", Path: "x"},
		"no path": {Name: "x", Path: ""},
		"a float": {Name: "x", Path: "x", Build: []manifest.BlockField{
			{Key: "backend", Value: "shell"}, {Key: "ratio", Value: 0.5},
		}},
		"no key": {Name: "x", Path: "x", Build: []manifest.BlockField{
			{Key: "", Value: "shell"},
		}},
		"not UTF-8": {Name: "x", Path: "x", Include: []string{"\xff"}},
	} {
		_, err := manifest.AppendComponent([]byte(editHeader), block)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "cannot declare the component", name)
	}
}

// TestAppendComponentTables checks that a field of a table, an array of
// tables and an integer of another type are written inline, the keys of a
// table in their order, and read back as the decoder reads them.
func TestAppendComponentTables(t *testing.T) {
	t.Parallel()

	out, err := manifest.AppendComponent([]byte(editHeader), manifest.ComponentBlock{
		Name: "calc", Path: "calc",
		Build: []manifest.BlockField{
			{Key: "backend", Value: "shell"}, {Key: "command", Value: "./build.sh"},
			{Key: "env", Value: map[string]string{"PROFILE": "release", "CC": "zig cc"}},
			{Key: "output", Value: []map[string]string{
				{"path": "dist/calc.wasm", "to": "share"}, {"path": "dist/calc"},
			}},
			{Key: "args", Value: []any{"-j", "4"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, editHeader+`
[components.calc]
path = "calc"

[components.calc.build]
backend = "shell"
command = "./build.sh"
env = { CC = "zig cc", PROFILE = "release" }
output = [{ path = "dist/calc.wasm", to = "share" }, { path = "dist/calc" }]
args = ["-j", "4"]
`, string(out))

	mfst := reparse(t, out)
	assert.Equal(t, map[string]string{"CC": "zig cc", "PROFILE": "release"},
		mfst.Components["calc"].Build.Env)
	assert.Equal(t, manifest.OutputList{
		{Path: "dist/calc.wasm", To: manifest.OutputToShare}, {Path: "dist/calc"},
	}, mfst.Components["calc"].Build.Output)
}

// editedSource is a manifest with two components the tests of EditComponent
// and RemoveComponent change, in the forms a user writes them.
const editedSource = editHeader + `
# The router, by tarawasm.
[components.router]
path = "router" # its directory
include = ["dist/router.wasm"]

[components.router.build]
backend = "shell"
command = "tarawasm"
args = [
  "all", # every step
  "--release",
]
incremental = true

[components.router.build.env]
PROFILE = "release"

[components.app]
path = "app"
`

// TestEditComponent checks that a field there has its value replaced where
// it is written, a multi-line array included, a new one goes after the last
// field of its table, a removed one takes its lines, and every other byte
// stays.
func TestEditComponent(t *testing.T) {
	t.Parallel()

	out, err := manifest.EditComponent([]byte(editedSource), manifest.ComponentEdit{
		Name:   "router",
		Fields: []manifest.BlockField{{Key: "path", Value: "wasm/router"}},
		Build: []manifest.BlockField{
			{Key: "args", Value: []string{"build"}},
			{Key: "output", Value: "dist/router.wasm"},
		},
		Remove: []string{"include", "build.incremental", "build.cwd"},
	})
	require.NoError(t, err)
	assert.Equal(t, editHeader+`
# The router, by tarawasm.
[components.router]
path = "wasm/router" # its directory

[components.router.build]
backend = "shell"
command = "tarawasm"
args = ["build"]
output = "dist/router.wasm"

[components.router.build.env]
PROFILE = "release"

[components.app]
path = "app"
`, string(out))

	mfst := reparse(t, out)
	assert.Equal(t, "wasm/router", mfst.Components["router"].Path)
	assert.Empty(t, mfst.Components["router"].Include)
	assert.Equal(t, manifest.OutputList{{Path: "dist/router.wasm"}},
		mfst.Components["router"].Build.Output)
	assert.False(t, mfst.Components["router"].Build.IncrementalValue())

	// A component with no build block gets one, after its table; the build
	// block goes with its tables.
	out, err = manifest.EditComponent(out, manifest.ComponentEdit{
		Name:   "app",
		Fields: []manifest.BlockField{{Key: "include", Value: []string{"*.lua"}}},
		Build:  []manifest.BlockField{{Key: "backend", Value: "make"}},
	})
	require.NoError(t, err)
	assert.Contains(t, string(out), `
[components.app]
path = "app"
include = ["*.lua"]

[components.app.build]
backend = "make"
`)

	out, err = manifest.EditComponent(out, manifest.ComponentEdit{
		Name: "router", Remove: []string{"build"},
	})
	require.NoError(t, err)
	assert.Equal(t, editHeader+`
# The router, by tarawasm.
[components.router]
path = "wasm/router" # its directory

[components.app]
path = "app"
include = ["*.lua"]

[components.app.build]
backend = "make"
`, string(out))
}

// TestEditComponentForms checks the forms of a component EditComponent
// follows - dotted keys, a component under [components] - and those it
// refuses: a field in an inline table, a table where a value goes, a
// component the manifest does not declare, a key of no field.
func TestEditComponentForms(t *testing.T) {
	t.Parallel()

	dotted := editHeader + "\n[components.a]\npath = \"a\"\nbuild.backend = \"shell\"\n" +
		"\n[products.p]\ncomponents = [\"a\"]\ndefault = true\n"

	out, err := manifest.EditComponent([]byte(dotted), manifest.ComponentEdit{
		Name: "a", Build: []manifest.BlockField{{Key: "command", Value: "./b.sh"}},
	})
	require.NoError(t, err)
	assert.Contains(t, string(out),
		"build.backend = \"shell\"\nbuild.command = \"./b.sh\"\n\n[products.p]")
	reparse(t, out)

	inline := editHeader + "\n[components.a]\npath = \"a\"\n" +
		"build = { backend = \"shell\", command = \"./b.sh\" }\n"

	for _, edit := range []manifest.ComponentEdit{
		{Name: "a", Build: []manifest.BlockField{{Key: "cwd", Value: "x"}}},
		{Name: "a", Remove: []string{"build.command"}},
	} {
		_, err = manifest.EditComponent([]byte(inline), edit)
		require.ErrorIs(t, err, manifest.ErrUnsupportedForm)
		assert.Contains(t, err.Error(), "components.a.build")
	}

	_, err = manifest.EditComponent([]byte(editedSource), manifest.ComponentEdit{
		Name: "router", Build: []manifest.BlockField{{Key: "env", Value: "x"}},
	})
	require.ErrorIs(t, err, manifest.ErrUnsupportedForm)

	_, err = manifest.EditComponent([]byte(editedSource), manifest.ComponentEdit{Name: "nope"})
	require.ErrorIs(t, err, manifest.ErrNoComponent)

	for _, edit := range []manifest.ComponentEdit{
		{Name: "app", Fields: []manifest.BlockField{{Key: "build", Value: "x"}}},
		{Name: "app", Remove: []string{"build.env.A"}},
		{Name: "app", Remove: []string{"."}},
		{Name: "app", Fields: []manifest.BlockField{{Key: "include", Value: 0.5}}},
	} {
		_, err = manifest.EditComponent([]byte(editedSource), edit)
		require.Error(t, err, edit)
	}
}

// TestRemoveComponent checks that a component goes with its tables and the
// blank lines above them, in either form, every other line kept.
func TestRemoveComponent(t *testing.T) {
	t.Parallel()

	out, err := manifest.RemoveComponent([]byte(editedSource), "router")
	require.NoError(t, err)
	assert.Equal(t, editHeader+`
# The router, by tarawasm.

[components.app]
path = "app"
`, string(out))
	reparse(t, out)

	out, err = manifest.RemoveComponent([]byte(editedSource), "app")
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSuffix(editedSource, "\n[components.app]\npath = \"app\"\n"),
		string(out))

	inline := editHeader + "\n[components]\na = { path = \"a\" }\nb = { path = \"b\" }\n"

	out, err = manifest.RemoveComponent([]byte(inline), "a")
	require.NoError(t, err)
	assert.Equal(t, editHeader+"\n[components]\nb = { path = \"b\" }\n", string(out))

	_, err = manifest.RemoveComponent([]byte(inline), "c")
	require.ErrorIs(t, err, manifest.ErrNoComponent)
}
