// Package cmake is the cmake build backend: it configures a CMake project
// and builds it out of its source, then copies the files output lists from
// the build tree into the output directory of the build.
//
// The source is the working directory of the build, the one with
// CMakeLists.txt. The build tree is the work directory of the build
// (build.Dirs.Work), in _build/ of the project, and stays there between
// builds: cmake --build rebuilds what changed only, and the configuration
// that runs first each time takes a changed variable. A tree configured for
// another source or in another place, as a moved project leaves it, cmake
// would refuse, and it is configured anew; so is every tree of a fresh
// build.
//
// A build is made without tt too (build.Backend.Exports): the script of its
// export configures and builds as Run does, into TT_BUILD_DIR of the
// script, and copies the files of output from there, so that LuaRocks
// builds the source rock of a package of cmake as tt does.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package cmake

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// Name is the name of the cmake backend.
const Name = "cmake"

// cmakeTool is the program the backend runs.
const cmakeTool = "cmake"

// DefaultBuildType is the CMAKE_BUILD_TYPE a build is configured with when
// it names none: optimized, as a release is, with the debug information a
// crash of Tarantool in the module is read with.
const DefaultBuildType = "RelWithDebInfo"

const (
	// buildTypeVariable is the variable build_type sets.
	buildTypeVariable = "CMAKE_BUILD_TYPE"
	// prefixPathVariable is the variable of the prefixes find_path,
	// find_library and find_package search: <prefix>/include for a header.
	prefixPathVariable = "CMAKE_PREFIX_PATH"
	// parallelLevel is the environment variable cmake --build takes its
	// parallel level from when it is given no --parallel.
	parallelLevel = "CMAKE_BUILD_PARALLEL_LEVEL"
	// cacheFile is the file of a build tree that records how it was
	// configured.
	cacheFile = "CMakeCache.txt"
)

const (
	// darwinOS is the GOOS spelling of macOS.
	darwinOS = "darwin"
	// linuxModuleExt is the extension CMake gives a MODULE library on every
	// platform but Windows.
	linuxModuleExt = ".so"
)

// errMissingOutput reports a file output lists that the build did not make.
var errMissingOutput = errors.New("not in the cmake build tree")

var (
	// variableRe is the shape of a key of variables: the name of a cache
	// variable, with its type after a colon if it is given one, as
	// -D<name>[:<type>]=<value> takes it. An = would end the name early; a
	// space and a leading dash are kept out of it as out of a target.
	variableRe = regexp.MustCompile(`^[A-Za-z0-9_.+][A-Za-z0-9_.+-]*(:[A-Z]+)?$`)
	// nameRe is the shape of a target and of a build type: no space, and no
	// leading dash, which cmake --build would take for an option.
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_.+][A-Za-z0-9_.+-]*$`)
)

// Backend returns the cmake backend.
func Backend() build.Backend {
	return build.Backend{
		Name:           Name,
		Produces:       build.Results(),
		Config:         func() any { return new(Config) },
		New:            build.Typed(newBuild),
		Incremental:    false,
		OutputFrom:     build.OutputBackend,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           false,
		// The project is configured with the toolchain of the machine; a
		// toolchain file of another platform is the project's to give.
		Cross: false,
		// The script of a build runs the cmake Run runs (Export).
		Exports: true,
	}
}

// Config are the fields of a cmake build block. The files output lists are
// relative to the build tree.
type Config struct {
	// Variables are the cache variables the configuration sets,
	// -D<name>=<value>.
	Variables map[string]string `toml:"variables"`
	// Targets are the targets built; all of them by default, as
	// cmake --build builds.
	Targets []string `toml:"targets"`
	// BuildType is the CMAKE_BUILD_TYPE; RelWithDebInfo by default.
	BuildType string `toml:"build_type"`
}

// newBuild checks a cmake build block. output is required: the build tree of
// cmake holds the objects, the caches and the generated files of the build
// as well, so the build takes from it the files output names and nothing
// else.
func newBuild(config *Config, _ build.Component, common build.Common) (build.Build, error) {
	if len(common.Output) == 0 {
		return nil, build.FieldErrorf("", "output is required for backend %q: the files of its "+
			"build tree the build takes", Name)
	}

	for _, output := range common.Output {
		switch {
		case output.Path == "":
			return nil, build.FieldErrorf("output", "must not contain an empty entry")
		case filepath.IsAbs(output.Path):
			return nil, build.FieldErrorf("output", "%q is not relative to the build tree",
				output.Path)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(config.Variables)) {
		if !variableRe.MatchString(name) {
			return nil, build.FieldErrorf("variables", "%q is not a variable name: letters, "+
				"digits and _.+-, not - first, then :<TYPE> if it has one", name)
		}

		if variable, _, _ := strings.Cut(name, ":"); variable == buildTypeVariable {
			return nil, build.FieldErrorf("variables", "%s is set by build_type",
				buildTypeVariable)
		}
	}

	for _, target := range config.Targets {
		if !nameRe.MatchString(target) {
			return nil, build.FieldErrorf("targets", "%q is not a target name", target)
		}
	}

	if config.BuildType != "" && !nameRe.MatchString(config.BuildType) {
		return nil, build.FieldErrorf("build_type", "%q is not a build type", config.BuildType)
	}

	return cmakeBuild{config: *config, output: common.Output}, nil
}

// cmakeBuild is a checked cmake build block.
type cmakeBuild struct {
	config Config
	output []build.OutputFile
}

// Produces names the types of the files of output, which are what the
// build makes.
func (b cmakeBuild) Produces() []build.Result {
	var results []build.Result

	for _, output := range b.output {
		if !slices.Contains(results, output.To) {
			results = append(results, output.To)
		}
	}

	return results
}

// Programs names the programs of output, by base name, which is what the
// build copies them as.
func (b cmakeBuild) Programs() ([]string, bool) {
	var names []string

	for _, output := range b.output {
		name := filepath.Base(output.Path)
		if output.To == build.Program && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names, true
}

// Preflight checks that cmake is in the PATH of the build.
func (b cmakeBuild) Preflight(_ context.Context, c *build.Context) error {
	_, err := c.LookPath(cmakeTool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH",
			build.ErrNoTool, Name, cmakeTool)
	}

	return nil
}

// Run configures the working directory into the build tree as
// cmake -S <cwd> -B <tree> -DCMAKE_BUILD_TYPE=<type> -D<name>=<value>...,
// builds it as cmake --build <tree> --config <type> --parallel <n>
// [--target <target>...], and copies the files of output into the output
// directory (see copyOutputs).
func (b cmakeBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err != nil {
		return err
	}

	tree := c.Dirs.Work

	if c.Fresh || staleTree(tree, c.Dirs.Cwd) {
		err = os.RemoveAll(tree)
		if err != nil {
			return fmt.Errorf("removing the build tree %s: %w", tree, err)
		}
	}

	err = c.Run(c.Command(ctx, cmakeTool,
		configureArgs(b.config, c.Dirs.Cwd, tree, c.Tarantool.Prefix)...))
	if err != nil {
		return err
	}

	// The number of jobs depends on the environment the command gets.
	cmd := c.Command(ctx, cmakeTool)

	cmd.Args = append(cmd.Args, buildArgs(b.config, tree, jobs(c.Jobs, cmd.Env))...)

	err = c.Run(cmd)
	if err != nil {
		return err
	}

	return b.copyOutputs(c)
}

// configureArgs builds the argv that configures source into tree. The
// variables come in the order of their names, after CMAKE_BUILD_TYPE.
//
// prefix, the install prefix of Tarantool when it is known, is added to
// CMAKE_PREFIX_PATH, after the prefixes the build lists there itself, if it
// does: that is where find_path(... tarantool/module.h) of the usual
// FindTarantool.cmake looks, as <prefix>/include.
func configureArgs(config Config, source, tree, prefix string) []string {
	args := []string{"-S", source, "-B", tree, "-D" + buildTypeVariable + "=" + buildType(config)}
	prefixed := prefix == ""

	for _, name := range slices.Sorted(maps.Keys(config.Variables)) {
		value := config.Variables[name]

		if variable, _, _ := strings.Cut(name, ":"); !prefixed && variable == prefixPathVariable {
			value = cmakeList(value, prefix)
			prefixed = true
		}

		args = append(args, "-D"+name+"="+value)
	}

	if !prefixed {
		args = append(args, "-D"+prefixPathVariable+"="+prefix)
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

// buildArgs builds the argv that builds the targets of config in tree, all
// of them unless it lists some, with jobs jobs; no --parallel when jobs is
// 0. --config selects the build type of a multi-config generator, and a
// single-config one ignores it.
func buildArgs(config Config, tree string, jobs int) []string {
	args := []string{"--build", tree, "--config", buildType(config)}

	if jobs > 0 {
		args = append(args, "--parallel", strconv.Itoa(jobs))
	}

	if len(config.Targets) > 0 {
		args = append(args, "--target")
		args = append(args, config.Targets...)
	}

	return args
}

// jobs is the number of jobs cmake --build is given: set, the Jobs of the
// build (build.Context.Jobs), as tt package build --jobs sets them, which
// win over CMAKE_BUILD_PARALLEL_LEVEL, being said for this build; otherwise
// one a CPU, unless CMAKE_BUILD_PARALLEL_LEVEL in environ says how many,
// which cmake reads when it is given none. A bare --parallel would not do:
// it runs make -j, as many jobs at once as there is work, which a large
// project with third-party libraries to build can run a machine out of
// memory with.
func jobs(set int, environ []string) int {
	if set > 0 {
		return set
	}

	level, ok := build.LookupEnv(environ, parallelLevel)
	if ok && level != "" {
		return 0
	}

	return runtime.NumCPU()
}

// buildType is the build type of config, RelWithDebInfo unless it names
// another.
func buildType(config Config) string {
	if config.BuildType == "" {
		return DefaultBuildType
	}

	return config.BuildType
}

// copyOutputs copies the files output lists, relative to the build tree,
// into the directories of their types under their base names, as the core
// copies those of a make build. A file the build did not make fails it, all
// of them named at once.
//
// On macOS, a .so module is copied as .dylib: CMake names a MODULE library
// .so there too, and Tarantool loads no .so on macOS. The c, lua-c and cargo
// backends name the module so as well, and output can name the file of every
// platform alike.
func (b cmakeBuild) copyOutputs(c *build.Context) error {
	tree := c.Dirs.Work

	var missing []string

	for _, output := range b.output {
		info, err := os.Stat(filepath.Join(tree, output.Path))
		if err != nil || !info.Mode().IsRegular() {
			missing = append(missing, strconv.Quote(output.Path))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("output %s: %w %s; output names the files the build "+
			"makes, relative to the build tree", strings.Join(missing, ", "),
			errMissingOutput, tree)
	}

	for _, output := range b.output {
		name := filepath.Base(output.Path)
		if output.To == build.Module && c.Target.OS == darwinOS {
			if stem, ok := strings.CutSuffix(name, linuxModuleExt); ok {
				name = stem + c.Target.ModuleExt()
			}
		}

		err := build.CopyFile(c.Dirs.OutputPath(output.To, name),
			filepath.Join(tree, output.Path))
		if err != nil {
			return fmt.Errorf("copy declared output %q: %w", output.Path, err)
		}
	}

	return nil
}

// staleTree reports whether the build tree at tree was configured for another
// source than source, or in another directory, as a project that was moved
// or a build whose cwd changed leaves it: cmake refuses to configure such a
// tree again. A tree with no cache is not stale.
func staleTree(tree, source string) bool {
	data, err := os.ReadFile(filepath.Join(tree, cacheFile))
	if err != nil {
		return false
	}

	want := map[string]string{
		"CMAKE_HOME_DIRECTORY": source,
		"CMAKE_CACHEFILE_DIR":  tree,
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
