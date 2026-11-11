package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// cmakeManifest builds a module and a program with cmake, and records what
// a build and a hook are told of Tarantool.
const cmakeManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[components.native]
path = 'native'

[components.native.build]
backend = 'cmake'
variables = { WITH_ANSWER = 'ON' }
targets = ['answer']
output = ['lib/answer.so']

[components.hello]
path = 'hello'
kind = 'executable'

[components.hello.build]
backend = 'cmake'
build_type = 'Release'
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

// answerCMakeLists is the CMakeLists.txt of a module that finds the headers
// of Tarantool as FindTarantool.cmake of the Tarantool modules does, through
// CMAKE_PREFIX_PATH, and builds answer.so into lib/ of the build tree.
const answerCMakeLists = `cmake_minimum_required(VERSION 3.15)
project(answer C)

find_path(TARANTOOL_INCLUDE_DIR tarantool/module.h)
if(NOT TARANTOOL_INCLUDE_DIR)
    message(FATAL_ERROR "tarantool/module.h not found")
endif()
if(NOT WITH_ANSWER)
    message(FATAL_ERROR "WITH_ANSWER is not set")
endif()

add_library(answer MODULE answer.c)
target_include_directories(answer PRIVATE ${TARANTOOL_INCLUDE_DIR}/tarantool)
set_target_properties(answer PROPERTIES PREFIX "" SUFFIX ".so"
    LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR}/lib)
`

// cmakeFiles is the project of cmakeManifest.
var cmakeFiles = map[string]string{
	"native/CMakeLists.txt": answerCMakeLists,
	"native/answer.c":       "#include <module.h>\nint answer(void) { return TT_ANSWER; }\n",
	"native/answer.lua":     "return require('my-app.answer')\n",
	"hello/CMakeLists.txt": "cmake_minimum_required(VERSION 3.15)\nproject(hello C)\n" +
		"add_executable(hello hello.c)\n",
	"hello/hello.c": "int main(void) { return 0; }\n",
	"probe/tell.sh": tellScript,
}

// requireCMake skips the test when cmake or a C compiler is not in PATH.
func requireCMake(t *testing.T) {
	t.Helper()

	for _, tool := range []string{"cmake", "cc"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not found in PATH", tool)
		}
	}
}

// fakeTarantoolPrefix writes an install prefix of Tarantool whose module.h
// is all the module of cmakeFiles needs to compile, and returns it.
func fakeTarantoolPrefix(t *testing.T) string {
	t.Helper()

	prefix := t.TempDir()
	writeFile(t, filepath.Join(prefix, "include", "tarantool", "module.h"),
		"#define TT_ANSWER 42\n")

	return prefix
}

func TestRun_cmakeComponents(t *testing.T) {
	t.Parallel()

	requireCMake(t)

	prefix := fakeTarantoolPrefix(t)
	includeDir := filepath.Join(prefix, "include", "tarantool")
	dir := setupProject(t, cmakeManifest, cmakeFiles)

	opts := dryOptions(dir)

	opts.Tarantool = rocks.TarantoolInfo{Prefix: prefix, Version: "3.0.0"}

	result, err := RunResult(context.Background(), opts)
	require.NoError(t, err)

	module := "answer.so"
	if runtime.GOOS == "darwin" {
		module = "answer.dylib"
	}

	tree := filepath.Join(dir, ".rocks")
	stage := packageStage(dir, "default")

	assert.FileExists(t, filepath.Join(stage, "lib", "my-app", module))
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", module))
	assert.FileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "answer.lua"))
	assert.FileExists(t, filepath.Join(tree, "bin", "hello"))
	assert.Equal(t, []string{"hello"}, result.Executables["hello"])

	for _, component := range []string{"native", "hello"} {
		assert.FileExists(t, filepath.Join(dir, "_build", "component", component,
			"CMakeCache.txt"))
	}

	assert.NoDirExists(t, filepath.Join(dir, "native", "CMakeFiles"),
		"the build is out of the source")

	want := prefix + "\n" + includeDir + "\n"

	told, err := os.ReadFile(filepath.Join(tree, "lib", "tarantool", "my-app", "tarantool.txt"))
	require.NoError(t, err)
	assert.Equal(t, want, string(told), "what a build is told of Tarantool")

	told, err = os.ReadFile(filepath.Join(dir, "probe", "tarantool.txt"))
	require.NoError(t, err)
	assert.Equal(t, want, string(told), "what a hook is told of Tarantool")

	// A second build rebuilds in the trees it kept.
	require.NoError(t, Run(context.Background(), opts))
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "my-app", module))
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
