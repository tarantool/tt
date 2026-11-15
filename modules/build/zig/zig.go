// Package zig is the zig build backend: zig build of the build.zig of the
// working directory, which installs what it built into a prefix of the
// build's own, out of the sources. The module the block names is copied from
// lib/ of the prefix into the directory of the modules of the build, the
// program it names from bin/ into the directory of the programs. A block that
// names module builds the module, one that names bin builds the program, and
// one that names both builds both, with one zig build.
//
// The build.zig is the project's: the backend runs its steps and takes what
// they installed by name, so it works with any build.zig, and with the
// helpers of zig-tarantool (addModule, installModule) a module is installed
// under the name Tarantool loads it by.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package zig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// Name is the name of the zig backend.
const Name = "zig"

// zigTool is the program the backend runs.
const zigTool = "zig"

const (
	// prefixDir is the directory of the work directory of the build zig build
	// installs into, its -p: lib/ and bin/ of it hold what it built.
	prefixDir = "zig-out"
	// cacheDir is the directory of the work directory of the build zig keeps
	// its cache of the project in, its --cache-dir.
	cacheDir = "zig-cache"
	// pkgDir is the directory zig 0.17 and later fetch the dependencies of
	// build.zig.zon into: by default zig-pkg/ beside build.zig.zon, in the
	// sources; the build gives zig the one of its work directory instead,
	// its --pkg-dir.
	pkgDir = "zig-pkg"
	// pkgDirEnv is the variable that names the directory of the packages
	// for zig: a build whose env sets it is given no --pkg-dir.
	pkgDirEnv = "ZIG_LOCAL_PKG_DIR"
)

// pkgDirSince is the first version of zig that keeps the packages it fetched
// in a directory of the project and takes --pkg-dir: zig 0.16 refuses it.
var pkgDirSince = [3]int{0, 17, 0}

// DefaultOptimize is the optimization mode a build is made in when its block
// names none.
const DefaultOptimize = "ReleaseSafe"

// darwinOS is the GOOS spelling of macOS.
const darwinOS = "darwin"

// releaseModes maps an optimization mode to the --release of zig build that
// asks for it; Debug, the mode of a zig build that is not asked for one,
// has none.
var releaseModes = map[string]string{
	"Debug":        "",
	"ReleaseSafe":  "safe",
	"ReleaseFast":  "fast",
	"ReleaseSmall": "small",
}

// zigArches maps a GOARCH to the architecture of a zig target triple, for a
// static program.
var zigArches = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

var (
	// ErrStaticDarwin reports a static program for darwin: macOS has no
	// static libSystem to link against, so a static program cannot be made
	// there.
	ErrStaticDarwin = errors.New("a static program cannot be built for darwin")
	// errNoArtifact reports a build that did not install what its block
	// names.
	errNoArtifact = errors.New("zig build installed no")
)

var (
	// programNameRe is the shape of the name of a program: a plain file
	// name, which tt instance finds in .rocks/bin/ by the binary of an
	// instance description given without a slash.
	programNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]*$`)
	// moduleRe is the shape of the module a zig build makes: names of
	// require and of the C functions of box.schema.func.create, joined by
	// dots, each a directory but the last, as installModule of zig-tarantool
	// installs a.b as lib/a/b.so.
	moduleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	// stepRe is the shape of the name of a step of zig build: not an option,
	// one word.
	stepRe = regexp.MustCompile(`^[^-\s][^\s]*$`)
	// optionRe is the shape of the name of an option of zig build, -D<name>.
	optionRe = regexp.MustCompile(`^[^=\s]+$`)
)

// Backend returns the zig backend.
func Backend() build.Backend {
	return build.Backend{
		Name:     Name,
		Produces: []build.Result{build.Module, build.Program},
		Config:   func() any { return new(Config) },
		New:      newBuild,
		// zig build keeps its own cache in the work directory of the build
		// and rebuilds what changed anyway; incremental skips running it.
		Incremental: true,
		OutputFrom:  build.OutputNone,
		// The prefix zig build run by hand in the component installs into:
		// what it holds is no file of the package, and the build takes what
		// it built from the prefix of its own. The packages zig build or zig
		// fetch run by hand fetch into the component are none either.
		Exclude:        []string{"/" + prefixDir + "/", "/" + pkgDir + "/"},
		NoSourceLayout: false,
		Hook:           false,
	}
}

// Config are the fields of a zig build block.
type Config struct {
	// Module is the module the build makes: the file of this name zig build
	// installs into lib/ of its prefix, <module>.so or lib<module>.so, a
	// dotted name a.b being lib/a/b.so.
	Module string `toml:"module"`
	// Bin is the program the build makes: the file of this name zig build
	// installs into bin/ of its prefix. A block of a component of
	// kind = "executable" (manifest_version 0.1, deprecated) that names
	// neither builds the program named after the component.
	Bin string `toml:"bin"`
	// Steps are the steps of zig build run, its default step, install, when
	// there are none.
	Steps []string `toml:"steps"`
	// Optimize is the optimization mode, ReleaseSafe by default: Debug,
	// ReleaseSafe, ReleaseFast or ReleaseSmall.
	Optimize string `toml:"optimize"`
	// Options are the options of the build.zig, -D<name>=<value>.
	Options map[string]string `toml:"options"`
}

// newBuild checks a zig build block of the component, the shapes of its
// fields, and picks what it builds: the module when the block names it, the
// program when it names it. One that names neither builds, for a component
// that wants programs alone (build.Component.Wants), the program named after
// the component, and is refused otherwise:
// zig build installs files of no name tt could tell.
func newBuild(cfg any, component build.Component, common build.Common) (build.Build, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
	}

	err := checkConfig(*config)
	if err != nil {
		return nil, err
	}

	bin := config.Bin

	if config.Module == "" && bin == "" {
		if !slices.Equal(component.Wants, []build.Result{build.Program}) {
			return nil, build.FieldErrorf("", "names neither module nor bin: "+
				"the build is to name what it makes, the module, the program or both")
		}

		bin = component.Name
	}

	return zigBuild{config: *config, module: config.Module, bin: bin, static: common.Static},
		nil
}

// checkConfig checks the shapes of the fields of a zig build.
func checkConfig(config Config) error {
	if config.Module != "" && !moduleRe.MatchString(config.Module) {
		return build.FieldErrorf("module", "%q is not a module name: names of a letter or _, "+
			"then letters, digits and _, joined by dots", config.Module)
	}

	if config.Bin != "" && !programNameRe.MatchString(config.Bin) {
		return build.FieldErrorf("bin", "%q is not a file name", config.Bin)
	}

	for i, step := range config.Steps {
		if !stepRe.MatchString(step) {
			return build.FieldErrorf(fmt.Sprintf("steps[%d]", i), "%q is not a step name", step)
		}
	}

	if _, ok := releaseModes[config.Optimize]; config.Optimize != "" && !ok {
		return build.FieldErrorf("optimize", "%q is not Debug, ReleaseSafe, ReleaseFast or "+
			"ReleaseSmall", config.Optimize)
	}

	for _, name := range sortedKeys(config.Options) {
		if !optionRe.MatchString(name) {
			return build.FieldErrorf("options", "%q is not an option name", name)
		}

		if name == "optimize" {
			return build.FieldErrorf("options.optimize", "is the field optimize of the block")
		}
	}

	return nil
}

// zigBuild is a checked zig build block.
type zigBuild struct {
	config Config
	// module is the module built; empty for none.
	module string
	// bin is the program built; empty for none.
	bin    string
	static bool
}

// Produces names what the build makes: a module, a program or both.
func (b zigBuild) Produces() []build.Result {
	var results []build.Result

	if b.module != "" {
		results = append(results, build.Module)
	}

	if b.bin != "" {
		results = append(results, build.Program)
	}

	return results
}

// Programs names the program the build makes, if it makes one.
func (b zigBuild) Programs() ([]string, bool) {
	if b.bin == "" {
		return nil, true
	}

	return []string{b.bin}, true
}

// Preflight checks that a static program is not asked for darwin and that
// zig is in the PATH of the build.
func (b zigBuild) Preflight(_ context.Context, c *build.Context) error {
	if b.static && b.bin != "" && c.Target.OS == darwinOS {
		return fmt.Errorf("%w: static = true needs a linux build", ErrStaticDarwin)
	}

	_, err := c.LookPath(zigTool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH",
			build.ErrNoTool, Name, zigTool)
	}

	return nil
}

// Inputs are what an incremental build reads besides the block and the files
// of the component: the version of zig, and build.zig and build.zig.zon of
// the working directory, which may be outside the component.
func (b zigBuild) Inputs(ctx context.Context, c *build.Context) ([]string, error) {
	version, err := c.Output(c.Command(ctx, zigTool, "version"))
	if err != nil {
		return nil, err
	}

	inputs := []string{"zig version " + strings.TrimSpace(string(version))}

	for _, name := range []string{"build.zig", "build.zig.zon"} {
		data, err := os.ReadFile(filepath.Join(c.Dirs.Cwd, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}

		sum := sha256.Sum256(data)

		inputs = append(inputs, name+" sha256:"+hex.EncodeToString(sum[:]))
	}

	return inputs, nil
}

// Run runs zig build in the working directory, installing into
// <work>/zig-out, keeping its cache in <work>/zig-cache and, for zig 0.17 and
// later, the packages it fetches in <work>/zig-pkg (see dirArgs), then copies
// the module from its lib/ (see copyModule) and the program from its bin/
// (see copyProgram), as the block asks for them.
func (b zigBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err != nil {
		return err
	}

	dirs, err := dirArgs(ctx, c)
	if err != nil {
		return err
	}

	target := ""

	if b.wantsMusl(c.Target) {
		accepts, err := b.acceptsTarget(ctx, c, dirs)
		if err != nil {
			return err
		}

		if accepts {
			target = zigArches[c.Target.Arch] + "-linux-musl"
		}
	}

	err = c.Run(c.Command(ctx, zigTool, b.args(c.Dirs.Work, dirs, target)...))
	if err != nil {
		return err
	}

	prefix := filepath.Join(c.Dirs.Work, prefixDir)

	if b.module != "" {
		err = copyModule(prefix, b.module, c.Dirs.Modules, c.Target.ModuleExt())
		if err != nil {
			return err
		}
	}

	if b.bin == "" {
		return nil
	}

	return copyProgram(prefix, b.bin, c.Dirs.Programs)
}

// wantsMusl reports whether the build asks for the program to be built for
// musl, which zig links in statically: a static build of a program alone,
// for linux, on an architecture zig is mapped for, whose options name no
// target of their own. A build of a module too is made for the target of the
// module, which is that of Tarantool, and its program is only checked.
func (b zigBuild) wantsMusl(platform build.Target) bool {
	_, mapped := zigArches[platform.Arch]
	_, own := b.config.Options["target"]

	return b.static && b.bin != "" && b.module == "" && platform.OS == "linux" && mapped && !own
}

// acceptsTarget reports whether the build.zig takes -Dtarget, as
// standardTargetOptions declares it: zig build refuses an option the
// build.zig does not declare. dirs are the directories of zig of the build
// (dirArgs): zig build --help runs the build.zig, and fetches its
// dependencies first.
func (b zigBuild) acceptsTarget(ctx context.Context, c *build.Context, dirs []string) (
	bool, error,
) {
	help, err := c.Output(c.Command(ctx, zigTool, append([]string{"build", "--help"},
		dirs...)...))
	if err != nil {
		return false, err
	}

	return strings.Contains(string(help), "-Dtarget="), nil
}

// dirArgs are the options of zig build that name the directories it keeps
// what it fetches and caches in, all in the work directory of the build, out
// of the sources: --cache-dir, and --pkg-dir for a zig that takes it
// (pkgDirSince), unless the environment of the build - tt's, or the env of
// the block - names a directory of the packages of its own,
// ZIG_LOCAL_PKG_DIR. Without one, zig 0.17 fetches the
// dependencies of build.zig.zon into zig-pkg/ beside it, which the layout
// leaves out (Backend.Exclude).
func dirArgs(ctx context.Context, c *build.Context) ([]string, error) {
	args := append(make([]string, 0, 4), //nolint:mnd // Two options of a value each.
		"--cache-dir", filepath.Join(c.Dirs.Work, cacheDir))

	cmd := c.Command(ctx, zigTool, "version")

	// The environment of a command of the build is tt's own with the block's
	// env over it, so a ZIG_LOCAL_PKG_DIR the user exported counts as one
	// the block sets.
	if value, _ := build.LookupEnv(cmd.Env, pkgDirEnv); value != "" {
		return args, nil
	}

	version, err := c.Output(cmd)
	if err != nil {
		return nil, err
	}

	if !takesPkgDir(strings.TrimSpace(string(version))) {
		return args, nil
	}

	return append(args, "--pkg-dir", filepath.Join(c.Dirs.Work, pkgDir)), nil
}

// takesPkgDir reports whether zig of version takes --pkg-dir: it is not older
// than pkgDirSince, a development version of that release being older. A
// version tt cannot read is taken to be older.
func takesPkgDir(version string) bool {
	version, _, _ = strings.Cut(version, "+")

	core, pre, _ := strings.Cut(version, "-")

	parts := strings.Split(core, ".")
	if len(parts) != len(pkgDirSince) {
		return false
	}

	numbers := make([]int, 0, len(parts))

	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return false
		}

		numbers = append(numbers, number)
	}

	if cmp := slices.Compare(numbers, pkgDirSince[:]); cmp != 0 {
		return cmp > 0
	}

	return pre == ""
}

// args builds the zig build argv of a build whose work directory is work,
// with the options of its directories dirs (dirArgs), for the zig target
// triple target, or for the host when it is empty.
func (b zigBuild) args(work string, dirs []string, target string) []string {
	args := append([]string{"build"}, b.config.Steps...)

	args = append(args, "-p", filepath.Join(work, prefixDir))
	args = append(args, dirs...)

	if mode := releaseModes[b.optimize()]; mode != "" {
		args = append(args, "--release="+mode)
	}

	for _, name := range sortedKeys(b.config.Options) {
		args = append(args, "-D"+name+"="+b.config.Options[name])
	}

	if target != "" {
		args = append(args, "-Dtarget="+target)
	}

	return args
}

// optimize is the optimization mode of the build, ReleaseSafe unless it
// names another.
func (b zigBuild) optimize() string {
	if b.config.Optimize == "" {
		return DefaultOptimize
	}

	return b.config.Optimize
}

// moduleCandidates are the names, relative to lib/ of the prefix, a module is
// looked for under, in order: as installModule of zig-tarantool installs it,
// <module>.so or <module>.dylib, then as installArtifact of a dynamic library
// does, lib<module>.so or lib<module>.dylib. A dotted name a.b is the file b
// of the directory a.
func moduleCandidates(module string) []string {
	parts := strings.Split(module, ".")
	dir, base := filepath.Join(parts[:len(parts)-1]...), parts[len(parts)-1]

	names := make([]string, 0, 4) //nolint:mnd // Two stems of two extensions.

	for _, stem := range []string{base, "lib" + base} {
		for _, ext := range []string{".so", ".dylib"} {
			names = append(names, filepath.Join(dir, stem+ext))
		}
	}

	return names
}

// copyModule copies the module zig build installed into lib/ of prefix,
// under one of the names of moduleCandidates, to <output>/<module><ext>, a
// dotted name being a path: ext is the extension Tarantool loads a module of
// the target by.
func copyModule(prefix, module, output, ext string) error {
	lib := filepath.Join(prefix, "lib")
	candidates := moduleCandidates(module)

	for _, name := range candidates {
		path := filepath.Join(lib, name)

		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		dst := filepath.Join(output, filepath.Join(strings.Split(module, ".")...)+ext)

		err = build.CopyFile(dst, path)
		if err != nil {
			return fmt.Errorf("copy the module %q: %w", path, err)
		}

		return nil
	}

	return fmt.Errorf("%w module %q: %s has none of %s (%s); install it with installModule "+
		"of zig-tarantool, or name the step that installs it in steps",
		errNoArtifact, module, lib, strings.Join(candidates, ", "), describeInstalled(lib))
}

// copyProgram copies the program bin zig build installed into bin/ of
// prefix to <programs>/<bin>.
func copyProgram(prefix, bin, programs string) error {
	dir := filepath.Join(prefix, "bin")
	path := filepath.Join(dir, bin)

	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w program %q: %s has no %s (%s); install it with "+
			"b.installArtifact, or name the step that installs it in steps",
			errNoArtifact, bin, dir, bin, describeInstalled(dir))
	}

	err = build.CopyFile(filepath.Join(programs, bin), path)
	if err != nil {
		return fmt.Errorf("copy the program %q: %w", path, err)
	}

	return nil
}

// maxListed is how many of the files a build installed a message names.
const maxListed = 10

// describeInstalled tells what files there are under dir, by their paths
// from it, for a message of a file the build did not install.
func describeInstalled(dir string) string {
	var files []string

	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // What cannot be read is not listed.
		}

		rel, err := filepath.Rel(dir, path)
		if err == nil {
			files = append(files, rel)
		}

		return nil
	})

	switch {
	case len(files) == 0:
		return "it installed nothing there"
	case len(files) > maxListed:
		return fmt.Sprintf("it installed %s and %d more", strings.Join(files[:maxListed], ", "),
			len(files)-maxListed)
	default:
		return "it installed " + strings.Join(files, ", ")
	}
}

// sortedKeys returns the keys of options in order.
func sortedKeys(options map[string]string) []string {
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
