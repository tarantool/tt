package cmake

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// writeFiles writes each file of files, by its path relative to dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
}

func TestConfigureArgs(t *testing.T) {
	t.Parallel()

	head := []string{"-S", "/src", "-B", "/b"}

	tests := []struct {
		name   string
		config Config
		prefix string
		want   []string
	}{
		{
			name:   "defaults",
			config: Config{},
			want:   []string{"-DCMAKE_BUILD_TYPE=RelWithDebInfo"},
		},
		{
			name:   "the prefix of Tarantool",
			config: Config{},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo", "-DCMAKE_PREFIX_PATH=/opt/tarantool",
			},
		},
		{
			name: "variables by name, after the build type",
			config: Config{
				BuildType: "Debug",
				Variables: map[string]string{
					"WITH_SASL":                 "OFF",
					"ENABLE_BUNDLED_SMALL:BOOL": "ON",
					"GREETING":                  "a b;c",
				},
			},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=Debug", "-DENABLE_BUNDLED_SMALL:BOOL=ON",
				"-DGREETING=a b;c", "-DWITH_SASL=OFF", "-DCMAKE_PREFIX_PATH=/opt/tarantool",
			},
		},
		{
			name:   "the prefix after those of the build",
			config: Config{Variables: map[string]string{"CMAKE_PREFIX_PATH": "/deps;/more"}},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo",
				"-DCMAKE_PREFIX_PATH=/deps;/more;/opt/tarantool",
			},
		},
		{
			name:   "the prefix after those of a typed variable",
			config: Config{Variables: map[string]string{"CMAKE_PREFIX_PATH:PATH": "/deps"}},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo",
				"-DCMAKE_PREFIX_PATH:PATH=/deps;/opt/tarantool",
			},
		},
		{
			name:   "the prefixes of the build alone",
			config: Config{Variables: map[string]string{"CMAKE_PREFIX_PATH": "/deps"}},
			want:   []string{"-DCMAKE_BUILD_TYPE=RelWithDebInfo", "-DCMAKE_PREFIX_PATH=/deps"},
		},
		{
			name:   "an empty prefix path",
			config: Config{Variables: map[string]string{"CMAKE_PREFIX_PATH": ""}},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo", "-DCMAKE_PREFIX_PATH=/opt/tarantool",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := configureArgs(tt.config, "/src", "/b", tt.prefix)
			assert.Equal(t, append(append([]string{}, head...), tt.want...), got)
		})
	}
}

func TestBuildArgs(t *testing.T) {
	t.Parallel()

	got := buildArgs(Config{}, "/b", 4)
	assert.Equal(t, []string{"--build", "/b", "--config", "RelWithDebInfo", "--parallel", "4"}, got)

	got = buildArgs(Config{BuildType: "Release", Targets: []string{"a", "b"}}, "/b", 0)
	assert.Equal(t, []string{"--build", "/b", "--config", "Release", "--target", "a", "b"}, got)
}

func TestJobs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, runtime.NumCPU(), jobs([]string{"PATH=/bin"}))
	assert.Equal(t, runtime.NumCPU(), jobs([]string{"CMAKE_BUILD_PARALLEL_LEVEL="}))
	assert.Zero(t, jobs([]string{"CMAKE_BUILD_PARALLEL_LEVEL=3"}))
	assert.Zero(t, jobs([]string{
		"CMAKE_BUILD_PARALLEL_LEVEL=", "CMAKE_BUILD_PARALLEL_LEVEL=2",
	}))

	// The level the env of the build sets is in the environment of its
	// commands, which is where the backend reads it.
	harness := buildtest.New(t, build.Component{
		Name: "m", Kind: build.KindModule, Path: "m", Namespace: "", Package: "",
	})

	harness.Context.Env[parallelLevel] = "2"

	assert.Zero(t, jobs(harness.Context.Command(t.Context(), cmakeTool).Env))
}

// writeCache writes a CMakeCache.txt into tree that records source and
// cacheDir as cmake does.
func writeCache(t *testing.T, tree, source, cacheDir string) {
	t.Helper()

	cache := "# This is the CMakeCache file.\n" +
		"CMAKE_BUILD_TYPE:STRING=RelWithDebInfo\n" +
		"CMAKE_CACHEFILE_DIR:INTERNAL=" + cacheDir + "\n" +
		"CMAKE_HOME_DIRECTORY:INTERNAL=" + source + "\n"
	writeFiles(t, tree, map[string]string{cacheFile: cache})
}

func TestStaleTree(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	other := t.TempDir()

	t.Run("no tree", func(t *testing.T) {
		t.Parallel()

		assert.False(t, staleTree(filepath.Join(t.TempDir(), "none"), source))
	})

	t.Run("the tree of the source", func(t *testing.T) {
		t.Parallel()

		tree := t.TempDir()
		writeCache(t, tree, source, tree)
		assert.False(t, staleTree(tree, source))
	})

	t.Run("through a symbolic link", func(t *testing.T) {
		t.Parallel()

		tree := t.TempDir()
		link := filepath.Join(t.TempDir(), "src")
		require.NoError(t, os.Symlink(source, link))
		writeCache(t, tree, link, tree)
		assert.False(t, staleTree(tree, source))
	})

	t.Run("another source", func(t *testing.T) {
		t.Parallel()

		tree := t.TempDir()
		writeCache(t, tree, other, tree)
		assert.True(t, staleTree(tree, source))
	})

	t.Run("a moved project", func(t *testing.T) {
		t.Parallel()

		tree := t.TempDir()
		writeCache(t, tree, "/gone/src", "/gone/_build/cmake/m")
		assert.True(t, staleTree(tree, source))
	})
}

// component is a component of kind.
func component(kind string) build.Component {
	return build.Component{Name: "m", Kind: kind, Path: "m", Namespace: "", Package: ""}
}

func TestCopyOutputs(t *testing.T) {
	t.Parallel()

	t.Run("names every missing file", func(t *testing.T) {
		t.Parallel()

		harness := buildtest.New(t, component(build.KindModule))
		tree := harness.Context.Dirs.Work

		writeFiles(t, tree, map[string]string{"lib/m.so": "so"})
		require.NoError(t, os.MkdirAll(filepath.Join(tree, "dir"), 0o750))

		bld := cmakeBuild{output: []string{"lib/m.so", "m2.so", "dir"}, module: true}

		err := bld.copyOutputs(harness.Context)
		require.ErrorIs(t, err, errMissingOutput)
		assert.Contains(t, err.Error(), `output "m2.so", "dir": not in the cmake build tree `+
			tree)
		assert.NoFileExists(t, filepath.Join(harness.Context.Dirs.Output, "m.so"))
	})

	tests := []struct {
		name string
		goos string
		kind string
		want string
	}{
		{"a module on linux", "linux", build.KindModule, "m.so"},
		{"a module on macOS", darwinOS, build.KindModule, "m.dylib"},
		{"a program on macOS", darwinOS, build.KindExecutable, "m.so"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, component(tt.kind))

			harness.Context.Target.OS = tt.goos
			writeFiles(t, harness.Context.Dirs.Work, map[string]string{"lib/m.so": "so"})

			bld := cmakeBuild{output: []string{"lib/m.so"}, module: tt.kind == build.KindModule}
			require.NoError(t, bld.copyOutputs(harness.Context))

			entries, err := os.ReadDir(harness.Context.Dirs.Output)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, tt.want, entries[0].Name())
		})
	}
}

// requireCMake skips the test when cmake or a C compiler is not in PATH.
func requireCMake(t *testing.T) {
	t.Helper()

	for _, tool := range []string{cmakeTool, "cc"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not found in PATH", tool)
		}
	}
}

// answerCMakeLists is the CMakeLists.txt of a module that finds the headers of
// Tarantool as FindTarantool.cmake of the Tarantool modules does, through
// CMAKE_PREFIX_PATH, checks that the build was told the same directory, and
// builds answer.so, as require takes it, into lib/.
const answerCMakeLists = `cmake_minimum_required(VERSION 3.15)
project(answer C)

find_path(TARANTOOL_INCLUDE_DIR tarantool/module.h)
if(NOT TARANTOOL_INCLUDE_DIR)
    message(FATAL_ERROR "tarantool/module.h not found")
endif()
if(NOT "$ENV{TT_TARANTOOL_INCLUDE_DIR}" STREQUAL "${TARANTOOL_INCLUDE_DIR}/tarantool")
    message(FATAL_ERROR "TT_TARANTOOL_INCLUDE_DIR is '$ENV{TT_TARANTOOL_INCLUDE_DIR}'")
endif()
if(NOT WITH_ANSWER)
    message(FATAL_ERROR "WITH_ANSWER is not set")
endif()

add_library(answer MODULE answer.c)
target_include_directories(answer PRIVATE ${TARANTOOL_INCLUDE_DIR}/tarantool)
set_target_properties(answer PROPERTIES PREFIX "" SUFFIX ".so"
    LIBRARY_OUTPUT_DIRECTORY ${CMAKE_BINARY_DIR}/lib)
add_library(unused MODULE answer.c)
target_include_directories(unused PRIVATE ${TARANTOOL_INCLUDE_DIR}/tarantool)
`

// answerC is the source of the module answer.
const answerC = "#include <module.h>\nint answer(void) { return TT_ANSWER; }\n"

// answerHarness returns a harness of the build of the module answer: its
// sources in the working directory, and a Tarantool whose module.h is one
// of its own, which is all the module needs to compile, as the core tells a
// build of it, in the Context and in TT_TARANTOOL_*.
func answerHarness(t *testing.T) *buildtest.Harness {
	t.Helper()

	harness := buildtest.New(t, build.Component{
		Name: "answer", Kind: build.KindModule, Path: "answer", Namespace: "demo", Package: "",
	})

	prefix := t.TempDir()
	includeDir := filepath.Join(prefix, "include", "tarantool")

	writeFiles(t, prefix, map[string]string{"include/tarantool/module.h": "#define TT_ANSWER 42\n"})
	writeFiles(t, harness.Context.Dirs.Cwd, map[string]string{
		"CMakeLists.txt": answerCMakeLists,
		"answer.c":       answerC,
	})

	harness.Context.Tarantool.Prefix = prefix
	harness.Context.Tarantool.IncludeDir = includeDir
	harness.Context.Env["TT_TARANTOOL_PREFIX"] = prefix
	harness.Context.Env["TT_TARANTOOL_INCLUDE_DIR"] = includeDir

	return harness
}

// answerBuild returns the checked build of the module answer, its block
// listing output.
func answerBuild(t *testing.T, output string, targets ...any) build.Build {
	t.Helper()

	rest := map[string]any{"variables": map[string]any{"WITH_ANSWER": "ON"}}
	if len(targets) > 0 {
		rest["targets"] = targets
	}

	bld, err := buildtest.Validate(Backend(), build.Component{
		Name: "answer", Kind: build.KindModule, Path: "answer", Namespace: "demo", Package: "",
	}, buildtest.Block{
		Common:      build.Common{Cwd: "", Env: nil, Output: []string{output}, Static: false},
		Incremental: false,
		Rest:        rest,
	})
	require.NoError(t, err)

	return bld
}

func TestBuildsAModule(t *testing.T) {
	t.Parallel()

	requireCMake(t)

	harness := answerHarness(t)
	bld := answerBuild(t, "lib/answer.so", "answer")
	dirs := harness.Context.Dirs
	built := filepath.Join(dirs.Output, "answer"+harness.Context.Target.ModuleExt())

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())

	assert.FileExists(t, built)
	assert.NoDirExists(t, filepath.Join(dirs.Cwd, "CMakeFiles"), "the build is out of the source")

	cache, err := os.ReadFile(filepath.Join(dirs.Work, cacheFile))
	require.NoError(t, err)
	assert.Contains(t, string(cache), "CMAKE_BUILD_TYPE:STRING=RelWithDebInfo\n")

	unused, err := filepath.Glob(filepath.Join(dirs.Work, "*unused*"))
	require.NoError(t, err)
	assert.Empty(t, unused, "only the targets listed are built")

	// The tree is kept: a build of nothing changed builds in it again.
	marker := filepath.Join(dirs.Work, "marker")
	require.NoError(t, os.WriteFile(marker, nil, 0o600))
	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.FileExists(t, marker)

	// A fresh build configures a new one.
	require.NoError(t, os.Remove(built))

	harness.Context.Fresh = true

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.NoFileExists(t, marker)
	assert.FileExists(t, built)
}

func TestFailsOnAMissingOutput(t *testing.T) {
	t.Parallel()

	requireCMake(t)

	harness := answerHarness(t)

	err := harness.Run(t.Context(), answerBuild(t, "answer.so"))
	require.ErrorIs(t, err, errMissingOutput)
	assert.Contains(t, err.Error(), `output "answer.so"`)
	assert.True(t, strings.HasSuffix(err.Error(), "relative to the build tree"), err.Error())
}

func TestConfiguresAMovedTreeAnew(t *testing.T) {
	t.Parallel()

	requireCMake(t)

	harness := answerHarness(t)
	dirs := harness.Context.Dirs

	writeCache(t, dirs.Work, "/gone/src", "/gone/_build/cmake/answer")

	require.NoError(t, harness.Run(t.Context(), answerBuild(t, "lib/answer.so")),
		harness.Printed())
	assert.False(t, staleTree(dirs.Work, dirs.Cwd))
}
