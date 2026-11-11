package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

func TestCMakeConfigureArgs(t *testing.T) {
	t.Parallel()

	head := []string{"-S", "/src", "-B", "/b"}

	tests := []struct {
		name   string
		build  manifest.Build
		prefix string
		want   []string
	}{
		{
			name:  "defaults",
			build: manifest.Build{Backend: BackendCMake},
			want:  []string{"-DCMAKE_BUILD_TYPE=RelWithDebInfo"},
		},
		{
			name:   "the prefix of Tarantool",
			build:  manifest.Build{Backend: BackendCMake},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo", "-DCMAKE_PREFIX_PATH=/opt/tarantool",
			},
		},
		{
			name: "variables by name, after the build type",
			build: manifest.Build{
				Backend:   BackendCMake,
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
			name: "the prefix after those of the build",
			build: manifest.Build{
				Backend:   BackendCMake,
				Variables: map[string]string{"CMAKE_PREFIX_PATH": "/deps;/more"},
			},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo",
				"-DCMAKE_PREFIX_PATH=/deps;/more;/opt/tarantool",
			},
		},
		{
			name: "the prefix after those of a typed variable",
			build: manifest.Build{
				Backend:   BackendCMake,
				Variables: map[string]string{"CMAKE_PREFIX_PATH:PATH": "/deps"},
			},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo",
				"-DCMAKE_PREFIX_PATH:PATH=/deps;/opt/tarantool",
			},
		},
		{
			name: "the prefixes of the build alone",
			build: manifest.Build{
				Backend:   BackendCMake,
				Variables: map[string]string{"CMAKE_PREFIX_PATH": "/deps"},
			},
			want: []string{"-DCMAKE_BUILD_TYPE=RelWithDebInfo", "-DCMAKE_PREFIX_PATH=/deps"},
		},
		{
			name: "an empty prefix path",
			build: manifest.Build{
				Backend:   BackendCMake,
				Variables: map[string]string{"CMAKE_PREFIX_PATH": ""},
			},
			prefix: "/opt/tarantool",
			want: []string{
				"-DCMAKE_BUILD_TYPE=RelWithDebInfo", "-DCMAKE_PREFIX_PATH=/opt/tarantool",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := cmakeConfigureArgs(tt.build, "/src", "/b", tt.prefix)
			assert.Equal(t, append(append([]string{}, head...), tt.want...), got)
		})
	}
}

func TestCMakeBuildArgs(t *testing.T) {
	t.Parallel()

	got := cmakeBuildArgs(manifest.Build{Backend: BackendCMake}, "/b", 4)
	assert.Equal(t, []string{"--build", "/b", "--config", "RelWithDebInfo", "--parallel", "4"}, got)

	build := manifest.Build{
		Backend: BackendCMake, BuildType: "Release", Targets: []string{"a", "b"},
	}

	got = cmakeBuildArgs(build, "/b", 0)
	assert.Equal(t, []string{"--build", "/b", "--config", "Release", "--target", "a", "b"}, got)
}

func TestCMakeJobs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, runtime.NumCPU(), cmakeJobs([]string{"PATH=/bin"}))
	assert.Equal(t, runtime.NumCPU(), cmakeJobs([]string{"CMAKE_BUILD_PARALLEL_LEVEL="}))
	assert.Zero(t, cmakeJobs([]string{"CMAKE_BUILD_PARALLEL_LEVEL=3"}))
	assert.Zero(t, cmakeJobs([]string{
		"CMAKE_BUILD_PARALLEL_LEVEL=", "CMAKE_BUILD_PARALLEL_LEVEL=2",
	}))
}

// writeCMakeCache writes a CMakeCache.txt into buildDir that records source
// and cacheDir as cmake does.
func writeCMakeCache(t *testing.T, buildDir, source, cacheDir string) {
	t.Helper()

	cache := "# This is the CMakeCache file.\n" +
		"CMAKE_BUILD_TYPE:STRING=RelWithDebInfo\n" +
		"CMAKE_CACHEFILE_DIR:INTERNAL=" + cacheDir + "\n" +
		"CMAKE_HOME_DIRECTORY:INTERNAL=" + source + "\n"
	writeFiles(t, buildDir, map[string]string{cmakeCacheFile: cache})
}

func TestStaleCMakeTree(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	other := t.TempDir()

	t.Run("no tree", func(t *testing.T) {
		t.Parallel()

		assert.False(t, staleCMakeTree(filepath.Join(t.TempDir(), "none"), source))
	})

	t.Run("the tree of the source", func(t *testing.T) {
		t.Parallel()

		buildDir := t.TempDir()
		writeCMakeCache(t, buildDir, source, buildDir)
		assert.False(t, staleCMakeTree(buildDir, source))
	})

	t.Run("through a symbolic link", func(t *testing.T) {
		t.Parallel()

		buildDir := t.TempDir()
		link := filepath.Join(t.TempDir(), "src")
		require.NoError(t, os.Symlink(source, link))
		writeCMakeCache(t, buildDir, link, buildDir)
		assert.False(t, staleCMakeTree(buildDir, source))
	})

	t.Run("another source", func(t *testing.T) {
		t.Parallel()

		buildDir := t.TempDir()
		writeCMakeCache(t, buildDir, other, buildDir)
		assert.True(t, staleCMakeTree(buildDir, source))
	})

	t.Run("a moved project", func(t *testing.T) {
		t.Parallel()

		buildDir := t.TempDir()
		writeCMakeCache(t, buildDir, "/gone/src", "/gone/_build/cmake/m")
		assert.True(t, staleCMakeTree(buildDir, source))
	})
}

func TestCopyCMakeOutputs(t *testing.T) {
	t.Parallel()

	t.Run("names every missing file", func(t *testing.T) {
		t.Parallel()

		buildDir := t.TempDir()
		writeFiles(t, buildDir, map[string]string{"lib/m.so": "so"})
		require.NoError(t, os.MkdirAll(filepath.Join(buildDir, "dir"), dirPerm))

		build := manifest.Build{Output: []string{"lib/m.so", "m2.so", "dir"}}
		env := Env{OutputDir: t.TempDir(), BuildDir: buildDir}

		err := copyCMakeOutputs(build, env)
		require.ErrorIs(t, err, errMissingOutput)
		assert.Contains(t, err.Error(), `output "m2.so", "dir": not in the cmake build tree `+
			buildDir)
		assert.NoFileExists(t, filepath.Join(env.OutputDir, "m.so"))
	})

	tests := []struct {
		name   string
		goos   string
		module bool
		want   string
	}{
		{"a module on linux", "linux", true, "m.so"},
		{"a module on macOS", darwinOS, true, "m.dylib"},
		{"a program on macOS", darwinOS, false, "m.so"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buildDir := t.TempDir()
			writeFiles(t, buildDir, map[string]string{"lib/m.so": "so"})

			out := filepath.Join(t.TempDir(), "lib", "ns")
			env := Env{OutputDir: out, BuildDir: buildDir, OS: tt.goos, Module: tt.module}

			require.NoError(t, copyCMakeOutputs(manifest.Build{Output: []string{"lib/m.so"}}, env))

			entries, err := os.ReadDir(out)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, tt.want, entries[0].Name())
		})
	}
}

func TestCMakeRunNeedsAnAbsoluteBuildDir(t *testing.T) {
	t.Parallel()

	build := manifest.Build{Backend: BackendCMake, Output: []string{"m.so"}}

	err := cmakeBackend{}.Run(context.Background(), build, t.TempDir(),
		Env{OutputDir: t.TempDir(), BuildDir: "_build/cmake/m"})
	require.ErrorIs(t, err, errRelativeBuildDir)
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

// fakeTarantoolPrefix writes an install prefix of Tarantool with a module.h
// of its own, which is all a module that includes it needs to compile, and
// returns it.
func fakeTarantoolPrefix(t *testing.T) string {
	t.Helper()

	prefix := t.TempDir()
	writeFiles(t, prefix, map[string]string{
		"include/tarantool/module.h": "#define TT_ANSWER 42\n",
	})

	return prefix
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

func TestCMakeBuildsAModule(t *testing.T) {
	t.Parallel()

	requireCMake(t)

	prefix := fakeTarantoolPrefix(t)
	source := t.TempDir()
	writeFiles(t, source, map[string]string{
		"CMakeLists.txt": answerCMakeLists,
		"answer.c":       answerC,
	})

	project := t.TempDir()
	buildDir := filepath.Join(project, "_build", "cmake", "answer")
	out := filepath.Join(t.TempDir(), "lib", "ns")

	build := manifest.Build{
		Backend:   BackendCMake,
		Variables: map[string]string{"WITH_ANSWER": "ON"},
		Targets:   []string{"answer"},
		Output:    []string{"lib/answer.so"},
	}
	env := Env{
		OutputDir:           out,
		ProjectRoot:         project,
		Component:           "answer",
		Module:              true,
		TarantoolPrefix:     prefix,
		TarantoolIncludeDir: filepath.Join(prefix, "include", "tarantool"),
		BuildDir:            buildDir,
	}

	require.NoError(t, cmakeBackend{}.Run(context.Background(), build, source, env))

	assert.FileExists(t, filepath.Join(out, "answer"+moduleExt(runtime.GOOS, linuxModuleExt)))
	assert.NoDirExists(t, filepath.Join(source, "CMakeFiles"), "the build is out of the source")

	cache, err := os.ReadFile(filepath.Join(buildDir, cmakeCacheFile))
	require.NoError(t, err)
	assert.Contains(t, string(cache), "CMAKE_BUILD_TYPE:STRING=RelWithDebInfo\n")

	unused, err := filepath.Glob(filepath.Join(buildDir, "*unused*"))
	require.NoError(t, err)
	assert.Empty(t, unused, "only the targets listed are built")

	// The tree is kept: a build of nothing changed builds in it again.
	marker := filepath.Join(buildDir, "marker")
	require.NoError(t, os.WriteFile(marker, nil, 0o600))
	require.NoError(t, cmakeBackend{}.Run(context.Background(), build, source, env))
	assert.FileExists(t, marker)

	// A fresh build configures a new one.
	env.Fresh = true
	require.NoError(t, cmakeBackend{}.Run(context.Background(), build, source, env))
	assert.NoFileExists(t, marker)
	assert.FileExists(t, filepath.Join(out, "answer"+moduleExt(runtime.GOOS, linuxModuleExt)))
}

func TestCMakeFailsOnAMissingOutput(t *testing.T) {
	t.Parallel()

	requireCMake(t)

	prefix := fakeTarantoolPrefix(t)
	source := t.TempDir()
	writeFiles(t, source, map[string]string{
		"CMakeLists.txt": answerCMakeLists,
		"answer.c":       answerC,
	})

	build := manifest.Build{
		Backend:   BackendCMake,
		Variables: map[string]string{"WITH_ANSWER": "ON"},
		Output:    []string{"answer.so"},
	}
	env := Env{
		OutputDir:           t.TempDir(),
		Module:              true,
		TarantoolPrefix:     prefix,
		TarantoolIncludeDir: filepath.Join(prefix, "include", "tarantool"),
		BuildDir:            filepath.Join(t.TempDir(), "answer"),
	}

	err := cmakeBackend{}.Run(context.Background(), build, source, env)
	require.ErrorIs(t, err, errMissingOutput)
	assert.Contains(t, err.Error(), `output "answer.so"`)
	assert.True(t, strings.HasSuffix(err.Error(), "relative to the build tree"), err.Error())
}

func TestCMakeConfiguresAMovedTreeAnew(t *testing.T) {
	t.Parallel()

	requireCMake(t)

	prefix := fakeTarantoolPrefix(t)
	source := t.TempDir()
	writeFiles(t, source, map[string]string{
		"CMakeLists.txt": answerCMakeLists,
		"answer.c":       answerC,
	})

	buildDir := filepath.Join(t.TempDir(), "answer")
	writeCMakeCache(t, buildDir, "/gone/src", "/gone/_build/cmake/answer")

	build := manifest.Build{
		Backend:   BackendCMake,
		Variables: map[string]string{"WITH_ANSWER": "ON"},
		Output:    []string{"lib/answer.so"},
	}
	env := Env{
		OutputDir:           t.TempDir(),
		Module:              true,
		TarantoolPrefix:     prefix,
		TarantoolIncludeDir: filepath.Join(prefix, "include", "tarantool"),
		BuildDir:            buildDir,
	}

	require.NoError(t, cmakeBackend{}.Run(context.Background(), build, source, env))
	assert.False(t, staleCMakeTree(buildDir, source))
}
