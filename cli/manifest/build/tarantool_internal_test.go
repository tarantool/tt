package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// treeManifest builds a module and a program with tree, a backend that
// copies the files output lists itself (buildregtest), and records what a
// build and a hook are told of Tarantool.
const treeManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.native]
path = 'native'

[components.native.build]
backend = 'tree'
output = ['lib/answer.so']

[components.hello]
path = 'hello'
kind = 'executable'

[components.hello.build]
backend = 'tree'
output = ['hello']

[components.probe]
path = 'probe'

[components.probe.build]
backend = 'shell'
command = 'sh'
args = ['tell.sh']

[hooks.pre_build]
backend = 'shell'
cwd = 'probe'
command = 'sh'
args = ['tell.sh']
`

// tellScript writes what a build is told of Tarantool into tarantool.txt of
// TT_OUTPUT_DIR, and a hook, which has none, into its working directory.
const tellScript = `printf '%s\n%s\n' "$TT_TARANTOOL_PREFIX" "$TT_TARANTOOL_INCLUDE_DIR" \
    > "${TT_OUTPUT_DIR:-.}/tarantool.txt"
`

// fakeTarantoolPrefix writes an install prefix of Tarantool with a module.h,
// and returns it.
func fakeTarantoolPrefix(t *testing.T) string {
	t.Helper()

	prefix := t.TempDir()
	writeFile(t, filepath.Join(prefix, "include", "tarantool", "module.h"),
		"#define TT_ANSWER 42\n")

	return prefix
}

// TestRun_backendOfItsOutput builds the components of a backend that copies
// what it built itself, a module and a program, and checks what a build and
// a hook are told of Tarantool.
func TestRun_backendOfItsOutput(t *testing.T) {
	t.Parallel()

	prefix := fakeTarantoolPrefix(t)
	includeDir := filepath.Join(prefix, "include", "tarantool")
	dir := setupProject(t, treeManifest, map[string]string{
		"native/lib/answer.so": "a module",
		"native/answer.lua":    "return require('my-app.answer')\n",
		"probe/tell.sh":        tellScript,
	})

	require.NoError(t, sdkbuild.CopyFile(filepath.Join(dir, "hello", "hello"), hostProgram(t)))

	opts := dryOptions(dir)

	opts.Tarantool = rocks.TarantoolInfo{Prefix: prefix, Version: "3.0.0"}

	result, err := RunResult(context.Background(), opts)
	require.NoError(t, err)

	tree := filepath.Join(dir, ".rocks")
	stage := packageStage(dir, "default")

	assert.FileExists(t, filepath.Join(stage, "lib", "my-app", "answer.so"))
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "answer.so"))
	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "answer.lua"))
	assert.FileExists(t, filepath.Join(tree, "bin", "hello"))
	assert.Equal(t, []string{"hello"}, result.Programs["hello"])

	want := prefix + "\n" + includeDir + "\n"

	told, err := os.ReadFile(filepath.Join(tree, "lib", "tarantool", "my-app", "tarantool.txt"))
	require.NoError(t, err)
	assert.Equal(t, want, string(told), "what a build is told of Tarantool")

	told, err = os.ReadFile(filepath.Join(dir, "probe", "tarantool.txt"))
	require.NoError(t, err)
	assert.Equal(t, want, string(told), "what a hook is told of Tarantool")

	// A second build copies them again.
	require.NoError(t, Run(context.Background(), opts))
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", "answer.so"))
}

func TestTarantoolDirs(t *testing.T) {
	t.Parallel()

	prefix := fakeTarantoolPrefix(t)

	gotPrefix, gotInclude := tarantoolDirs(rocks.TarantoolInfo{Prefix: prefix})
	assert.Equal(t, prefix, gotPrefix)
	assert.Equal(t, filepath.Join(prefix, "include", "tarantool"), gotInclude)

	bare := t.TempDir()

	gotPrefix, gotInclude = tarantoolDirs(rocks.TarantoolInfo{Prefix: bare})
	assert.Equal(t, bare, gotPrefix)
	assert.Empty(t, gotInclude, "a prefix without the headers has no include directory")

	gotPrefix, gotInclude = tarantoolDirs(rocks.TarantoolInfo{Executable: "/usr/bin/tarantool"})
	assert.Empty(t, gotPrefix)
	assert.Empty(t, gotInclude)
}

// TestBuilderTellsTheExecutable checks that a build is told the tarantool
// tt found, with its prefix, headers and version.
func TestBuilderTellsTheExecutable(t *testing.T) {
	t.Parallel()

	prefix := fakeTarantoolPrefix(t)
	opts := Options{Tarantool: rocks.TarantoolInfo{
		Executable: "/opt/bin/tarantool", Prefix: prefix, Version: "3.3.5",
	}}

	builder := newBuilder(opts, project{}, t.TempDir(), version.Version{})
	assert.Equal(t, sdkbuild.Tarantool{
		Prefix: prefix, IncludeDir: filepath.Join(prefix, "include", "tarantool"),
		Version: "3.3.5", Executable: "/opt/bin/tarantool",
	}, builder.tarantool)
}
