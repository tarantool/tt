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
