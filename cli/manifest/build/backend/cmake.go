package backend

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// cmakeTool is the program the cmake backend runs.
const cmakeTool = "cmake"

const (
	// cmakeBuildType is the variable build_type sets.
	cmakeBuildType = "CMAKE_BUILD_TYPE"
	// cmakePrefixPath is the variable of the prefixes find_path,
	// find_library and find_package search: <prefix>/include for a header.
	cmakePrefixPath = "CMAKE_PREFIX_PATH"
	// cmakeParallelLevel is the environment variable cmake --build takes
	// its parallel level from when it is given no --parallel.
	cmakeParallelLevel = "CMAKE_BUILD_PARALLEL_LEVEL"
	// cmakeCacheFile is the file of a build tree that records how it was
	// configured.
	cmakeCacheFile = "CMakeCache.txt"
)

var (
	// errRelativeBuildDir reports a build tree that is not an absolute path.
	errRelativeBuildDir = errors.New("build directory must be an absolute path")
	// errMissingOutput reports a file output lists that the build did not
	// make.
	errMissingOutput = errors.New("not in the cmake build tree")
)

// cmakeBackend configures a CMake project and builds it out of its source,
// then copies the files output lists from the build tree into TT_OUTPUT_DIR.
//
// The source is the working directory of the build, the one with
// CMakeLists.txt. The build tree is env.BuildDir, in _build/ of the project,
// and stays there between builds: cmake --build rebuilds what changed only,
// and the configuration that runs first each time takes a changed variable.
// A tree configured for another source or in another place, as a moved
// project leaves it, cmake would refuse, and it is configured anew; so is
// every tree with env.Fresh.
type cmakeBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// Run configures the source cwd into the build tree as
// cmake -S <cwd> -B <tree> -DCMAKE_BUILD_TYPE=<type> -D<name>=<value>...,
// builds it as cmake --build <tree> --config <type> --parallel <n>
// [--target <target>...], and copies the files of output into
// $TT_OUTPUT_DIR (see copyCMakeOutputs).
func (c cmakeBackend) Run(ctx context.Context, build manifest.Build, cwd string, env Env) error {
	err := requireAbsPaths(cwd, env.OutputDir)
	if err != nil {
		return err
	}

	if !filepath.IsAbs(env.BuildDir) {
		return fmt.Errorf("%w, got %q", errRelativeBuildDir, env.BuildDir)
	}

	err = Preflight(build, env)
	if err != nil {
		return err
	}

	if env.Fresh || staleCMakeTree(env.BuildDir, cwd) {
		err = os.RemoveAll(env.BuildDir)
		if err != nil {
			return fmt.Errorf("removing the build tree %s: %w", env.BuildDir, err)
		}
	}

	environ := env.environ()

	err = runEnviron(ctx, cwd, environ, c.showOutput, cmakeTool,
		cmakeConfigureArgs(build, cwd, env.BuildDir, env.TarantoolPrefix)...)
	if err != nil {
		return err
	}

	err = runEnviron(ctx, cwd, environ, c.showOutput, cmakeTool,
		cmakeBuildArgs(build, env.BuildDir, cmakeJobs(environ))...)
	if err != nil {
		return err
	}

	return copyCMakeOutputs(build, env)
}

// cmakeConfigureArgs builds the argv that configures source into buildDir.
// The variables come in the order of their names, after CMAKE_BUILD_TYPE.
//
// prefix, the install prefix of Tarantool when it is known, is added to
// CMAKE_PREFIX_PATH, after the prefixes the build lists there itself, if it
// does: that is where find_path(... tarantool/module.h) of the usual
// FindTarantool.cmake looks, as <prefix>/include.
func cmakeConfigureArgs(build manifest.Build, source, buildDir, prefix string) []string {
	args := []string{"-S", source, "-B", buildDir, "-D" + cmakeBuildType + "=" + cmakeType(build)}
	prefixed := prefix == ""

	for _, name := range slices.Sorted(maps.Keys(build.Variables)) {
		value := build.Variables[name]

		if variable, _, _ := strings.Cut(name, ":"); !prefixed && variable == cmakePrefixPath {
			value = cmakeList(value, prefix)
			prefixed = true
		}

		args = append(args, "-D"+name+"="+value)
	}

	if !prefixed {
		args = append(args, "-D"+cmakePrefixPath+"="+prefix)
	}

	return args
}

// cmakeList appends item to the CMake list list: items separated by ;.
func cmakeList(list, item string) string {
	if list == "" {
		return item
	}

	return list + ";" + item
}

// cmakeBuildArgs builds the argv that builds the targets of build in
// buildDir, all of them unless it lists some, with jobs jobs; no --parallel
// when jobs is 0. --config selects the build type of a multi-config
// generator, and a single-config one ignores it.
func cmakeBuildArgs(build manifest.Build, buildDir string, jobs int) []string {
	args := []string{"--build", buildDir, "--config", cmakeType(build)}

	if jobs > 0 {
		args = append(args, "--parallel", strconv.Itoa(jobs))
	}

	if len(build.Targets) > 0 {
		args = append(args, "--target")
		args = append(args, build.Targets...)
	}

	return args
}

// cmakeJobs is the number of jobs cmake --build is given: one a CPU, unless
// CMAKE_BUILD_PARALLEL_LEVEL in environ says how many, which cmake reads
// when it is given none. A bare --parallel would not do: it runs make -j,
// as many jobs at once as there is work, which a large project with
// third-party libraries to build can run a machine out of memory with.
func cmakeJobs(environ []string) int {
	level, ok := lookupEnv(environ, cmakeParallelLevel)
	if ok && level != "" {
		return 0
	}

	return runtime.NumCPU()
}

// cmakeType is the build type of build, RelWithDebInfo unless it names
// another.
func cmakeType(build manifest.Build) string {
	if build.BuildType == "" {
		return manifest.DefaultCMakeBuildType
	}

	return build.BuildType
}

// copyCMakeOutputs copies the files output lists, relative to the build
// tree, into env.OutputDir under their base names, as copyOutputs does those
// of a make build. A file the build did not make fails it, all of them named
// at once.
//
// On macOS, a .so of a module component is copied as .dylib: CMake names a
// MODULE library .so there too, and Tarantool loads no .so on macOS. The c,
// lua-c and cargo backends name the module so as well, and output can name
// the file of every platform alike.
func copyCMakeOutputs(build manifest.Build, env Env) error {
	var missing []string

	for _, output := range build.Output {
		info, err := os.Stat(filepath.Join(env.BuildDir, output))
		if err != nil || !info.Mode().IsRegular() {
			missing = append(missing, strconv.Quote(output))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("output %s: %w %s; output names the files the build "+
			"makes, relative to the build tree", strings.Join(missing, ", "),
			errMissingOutput, env.BuildDir)
	}

	err := os.MkdirAll(env.OutputDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create output directory %q: %w", env.OutputDir, err)
	}

	for _, output := range build.Output {
		name := filepath.Base(output)
		if env.Module && env.platformOS() == darwinOS {
			if stem, ok := strings.CutSuffix(name, linuxModuleExt); ok {
				name = stem + darwinModuleExt
			}
		}

		err = copyFile(filepath.Join(env.BuildDir, output), filepath.Join(env.OutputDir, name))
		if err != nil {
			return fmt.Errorf("copy declared output %q: %w", output, err)
		}
	}

	return nil
}

// staleCMakeTree reports whether the build tree buildDir was configured for
// another source than source, or in another directory, as a project that
// was moved or a build whose cwd changed leaves it: cmake refuses to
// configure such a tree again. A tree with no cache is not stale.
func staleCMakeTree(buildDir, source string) bool {
	data, err := os.ReadFile(filepath.Join(buildDir, cmakeCacheFile))
	if err != nil {
		return false
	}

	want := map[string]string{
		"CMAKE_HOME_DIRECTORY": source,
		"CMAKE_CACHEFILE_DIR":  buildDir,
	}

	for line := range strings.Lines(string(data)) {
		entry, value, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		if !ok {
			continue
		}

		name, _, _ := strings.Cut(entry, ":")

		dir, tracked := want[name]
		if tracked && !sameDir(value, dir) {
			return true
		}
	}

	return false
}

// sameDir reports whether the paths recorded and dir are one directory,
// through symbolic links as cmake compares them.
func sameDir(recorded, dir string) bool {
	recordedInfo, err := os.Stat(recorded)
	if err != nil {
		return false
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		return false
	}

	return os.SameFile(recordedInfo, dirInfo)
}
