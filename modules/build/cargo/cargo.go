// Package cargo is the cargo build backend: the library of a Rust package
// built as a cdylib, which Tarantool loads as a native module, copied into
// the directory of the modules of the build under the name module gives
// it, and a binary of the package, copied into the directory of the
// programs under its bin name. A block that names module builds the
// module, one that names bin builds the binary, one that names both builds
// both, and one that names neither builds the library under its own name.
//
// Where cargo puts what it built depends on the target, the profile and the
// target directory the project or the environment configures, so the paths
// are taken from what cargo reports with --message-format, never guessed.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package cargo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// Name is the name of the cargo backend.
const Name = "cargo"

// cargoTool is the program the backend runs.
const cargoTool = "cargo"

// DefaultProfile is the cargo profile a build is made with when its block
// names none.
const DefaultProfile = "release"

// targetDirName is the directory of the state of the build of a project
// (build.Dirs.State) the cargo builds of the project share as their target
// directory, as cargo builds the packages of a workspace in one: a module
// and a program of one crate, or of one workspace, share the dependencies
// compiled once. Cargo keeps what it builds of each package and profile
// apart in it, and locks it while it builds.
const targetDirName = "cargo"

const (
	// darwinOS is the GOOS spelling of macOS.
	darwinOS = "darwin"
	// linuxModuleExt and darwinModuleExt are the extensions of the library
	// cargo builds.
	linuxModuleExt  = ".so"
	darwinModuleExt = ".dylib"
)

// cargoArches maps a GOARCH to the architecture of a Rust target triple.
var cargoArches = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

var (
	// ErrStaticDarwin reports a static program for darwin: macOS has no
	// static libSystem to link against, so a static program cannot be made
	// there.
	ErrStaticDarwin = errors.New("a static program cannot be built for darwin")
	// errUnsupportedTarget reports a platform cargo is not mapped to a target
	// for.
	errUnsupportedTarget = errors.New("no cargo target for the platform")
	// errNoArtifact reports a cargo build that reported nothing it was asked
	// to build.
	errNoArtifact = errors.New("cargo reported no executable")
)

var (
	// programNameRe is the shape of the name of a program: a plain file
	// name, which tt instance finds in .rocks/bin/ by the binary of an
	// instance description given without a slash.
	programNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]*$`)
	// profileRe is the shape cargo allows a profile name.
	profileRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// moduleRe is the shape of the module a cargo build makes: the file is
	// <module>.so in the namespace of the component, and the module is a
	// name of require and of the C functions of box.schema.func.create, so
	// no dot.
	moduleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Backend returns the cargo backend.
func Backend() build.Backend {
	return build.Backend{
		Name:        Name,
		Produces:    []build.Result{build.Module, build.Program},
		Config:      func() any { return new(Config) },
		New:         build.Typed(newBuild),
		Incremental: false,
		OutputFrom:  build.OutputNone,
		// The target directory cargo makes in the component when it is run
		// by hand holds the library and the proc-macros of the
		// dependencies, which are no files of the package: the build takes
		// what it built from the target directory it builds in.
		Exclude:        []string{"/target/"},
		NoSourceLayout: false,
		Hook:           false,
	}
}

// Config are the fields of a cargo build block.
type Config struct {
	// Module builds the library of the package as the module of this
	// name. A block that names neither module nor bin builds the library
	// under its own name.
	Module string `toml:"module"`
	// Package is the cargo package that holds the binary or the library.
	Package string `toml:"package"`
	// Bin builds the binary of this name, the program of the same name.
	// A block of a component of kind = "executable" (manifest_version 0.1,
	// deprecated) that names neither builds the binary named after the
	// component.
	Bin string `toml:"bin"`
	// Features are the features of the package enabled.
	Features []string `toml:"features"`
	// Profile is the profile built, release by default.
	Profile string `toml:"profile"`
}

// newBuild checks a cargo build block of the component, the shapes of its
// fields, and picks what it builds: the module when the block names it,
// the binary when it names it. One that names neither builds the library
// under its own name, or, for a component that wants programs alone
// (build.Component.Wants), the binary named after the component.
func newBuild(config *Config, component build.Component, common build.Common) (
	build.Build, error,
) {
	err := checkConfig(*config)
	if err != nil {
		return nil, err
	}

	module, bin := config.Module != "", config.Bin

	if !module && bin == "" {
		if slices.Equal(component.Wants, []build.Result{build.Program}) {
			bin = component.Name
		} else {
			module = true
		}
	}

	return cargoBuild{config: *config, bin: bin, module: module, static: common.Static}, nil
}

// checkConfig checks the shapes of the fields of a cargo build.
func checkConfig(config Config) error {
	if config.Bin != "" && !programNameRe.MatchString(config.Bin) {
		return build.FieldErrorf("bin", "%q is not a file name", config.Bin)
	}

	if config.Module != "" && !moduleRe.MatchString(config.Module) {
		return build.FieldErrorf("module", "%q is not a module name: a letter or _, "+
			"then letters, digits and _", config.Module)
	}

	for _, feature := range config.Features {
		if feature == "" || strings.ContainsAny(feature, ", \t\n") {
			return build.FieldErrorf("features", "%q is not a feature name", feature)
		}
	}

	if config.Profile != "" && !profileRe.MatchString(config.Profile) {
		return build.FieldErrorf("profile", "%q is not a profile name", config.Profile)
	}

	return nil
}

// cargoBuild is a checked cargo build block.
type cargoBuild struct {
	config Config
	// bin is the binary built; empty for none.
	bin string
	// module is set for a build of the library, as a module.
	module bool
	static bool
}

// Produces names what the build makes: a module, a program or both.
func (b cargoBuild) Produces() []build.Result {
	var results []build.Result

	if b.module {
		results = append(results, build.Module)
	}

	if b.bin != "" {
		results = append(results, build.Program)
	}

	return results
}

// Programs names the program the build makes, if it builds one.
func (b cargoBuild) Programs() ([]string, bool) {
	if b.bin == "" {
		return nil, true
	}

	return []string{b.bin}, true
}

// Preflight checks that a static program is not asked for darwin, that the
// platform has a target and that cargo is in the PATH of the build.
func (b cargoBuild) Preflight(_ context.Context, c *build.Context) error {
	if b.static && b.bin != "" && c.Target.OS == darwinOS {
		return fmt.Errorf("%w: static = true needs a linux build", ErrStaticDarwin)
	}

	_, err := b.target(c.Target, b.bin != "")
	if err != nil {
		return err
	}

	_, err = c.LookPath(cargoTool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH",
			build.ErrNoTool, Name, cargoTool)
	}

	return nil
}

// Run builds the module (see runModule), then the binary (see runBinary),
// as the block asks for them.
func (b cargoBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err != nil {
		return err
	}

	if b.module {
		err = b.runModule(ctx, c)
		if err != nil {
			return err
		}
	}

	if b.bin == "" {
		return nil
	}

	return b.runBinary(ctx, c)
}

// runBinary builds the binary in the working directory as
// cargo build --profile <profile> --bin <bin> and copies it to
// <programs>/<bin>.
func (b cargoBuild) runBinary(ctx context.Context, c *build.Context) error {
	target, err := b.target(c.Target, true)
	if err != nil {
		return err
	}

	stdout, err := c.Output(b.command(ctx, c, b.args(target, c.Jobs)))
	if err != nil {
		if b.static {
			return fmt.Errorf("%w; a static build is made for the target %s, "+
				"which rustup target add %s installs", err, target, target)
		}

		return err
	}

	executable, err := findExecutable(stdout, b.bin)
	if err != nil {
		return err
	}

	err = build.CopyFile(filepath.Join(c.Dirs.Programs, b.bin), executable)
	if err != nil {
		return fmt.Errorf("copy the binary %q: %w", executable, err)
	}

	return nil
}

// runModule builds the library of the package in the working directory as a
// cdylib, which Tarantool loads as a native module - with require, and as
// the module of C stored procedures - and copies it to
// <output>/<module><ext>: module is the name of the library unless the
// block names another, and the extension .so, .dylib on macOS.
//
// cargo rustc --crate-type cdylib builds a cdylib whatever crate-type the
// package declares, and passes the link arguments of macOS to the library
// alone: its symbols of Tarantool are left to the loader, -undefined
// dynamic_lookup, as those of a C module are, and the dependencies are built
// as cargo builds them anyway.
func (b cargoBuild) runModule(ctx context.Context, c *build.Context) error {
	target, err := b.target(c.Target, false)
	if err != nil {
		return err
	}

	stdout, err := c.Output(b.command(ctx, c, b.moduleArgs(target, c.Target.OS, c.Jobs)))
	if err != nil {
		return err
	}

	library, name, err := findLibrary(stdout)
	if err != nil {
		return err
	}

	module := b.config.Module
	if module == "" {
		module = name
	}

	err = build.CopyFile(filepath.Join(c.Dirs.Modules, module+c.Target.ModuleExt()), library)
	if err != nil {
		return fmt.Errorf("copy the library %q: %w", library, err)
	}

	return nil
}

// targetDirKeys are the environment variables that name the target
// directory of cargo: CARGO_TARGET_DIR, and the build.target-dir setting of
// the cargo configuration as an environment variable.
var targetDirKeys = []string{"CARGO_TARGET_DIR", "CARGO_BUILD_TARGET_DIR"}

// command is cargo with args in c, with CARGO_TARGET_DIR in the state of the
// build of the project, so that the build leaves no target/ in the sources -
// another checkout's, for a path dependency. A target directory the
// environment of the build names, in env of the block or the environment tt
// runs in, is the user's choice and is kept; build.target-dir of the cargo
// configuration of the sources gives way, CARGO_TARGET_DIR winning over it.
// Where the target directory is makes no difference to the backend: it
// takes what cargo built from the paths cargo reports.
func (b cargoBuild) command(ctx context.Context, c *build.Context, args []string) *exec.Cmd {
	cmd := c.Command(ctx, cargoTool, args...)

	for _, key := range targetDirKeys {
		if value, ok := build.LookupEnv(cmd.Env, key); ok && value != "" {
			return cmd
		}
	}

	cmd.Env = append(cmd.Env, "CARGO_TARGET_DIR="+filepath.Join(c.Dirs.State, targetDirName))

	return cmd
}

// args builds the cargo build argv, with the number of jobs of jobsArgs.
// Diagnostics are rendered by cargo to stderr as usual, and only the JSON
// messages go to stdout.
func (b cargoBuild) args(target string, jobs int) []string {
	args := append([]string{
		"build", "--profile", b.profile(), "--bin", b.bin,
		"--message-format=json-render-diagnostics",
	}, b.selection(target)...)

	return append(args, jobsArgs(jobs)...)
}

// moduleArgs builds the cargo rustc argv of the library of a module
// component, for goos, with the number of jobs of jobsArgs.
func (b cargoBuild) moduleArgs(target, goos string, jobs int) []string {
	args := []string{
		"rustc", "--lib", "--crate-type", "cdylib", "--profile", b.profile(),
		"--message-format=json-render-diagnostics",
	}

	args = append(args, b.selection(target)...)
	args = append(args, jobsArgs(jobs)...)

	if goos == darwinOS {
		args = append(args, "--", "-C", "link-arg=-undefined", "-C", "link-arg=dynamic_lookup")
	}

	return args
}

// jobsArgs is the part of a cargo argv that says how many jobs it runs at
// once: -j <jobs>, the Jobs of the build (build.Context.Jobs), when they
// are set, and nothing otherwise, so that cargo runs as many as its own
// configuration says, build.jobs or CARGO_BUILD_JOBS, one a CPU by default.
func jobsArgs(jobs int) []string {
	if jobs <= 0 {
		return nil
	}

	return []string{"-j", strconv.Itoa(jobs)}
}

// profile is the profile of the build, release unless it names another.
func (b cargoBuild) profile() string {
	if b.config.Profile == "" {
		return DefaultProfile
	}

	return b.config.Profile
}

// selection is the part of a cargo argv that selects what to build: the
// package, the features and the target.
func (b cargoBuild) selection(target string) []string {
	var args []string

	if b.config.Package != "" {
		args = append(args, "--package", b.config.Package)
	}

	if len(b.config.Features) > 0 {
		args = append(args, "--features", strings.Join(b.config.Features, ","))
	}

	if target != "" {
		args = append(args, "--target", target)
	}

	return args
}

// target picks the --target of the build of the binary, for program, or of
// the module for platform, or none when cargo's own host target does.
//
// A static linux program is built for <arch>-unknown-linux-musl, which links
// the C library in: the gnu targets link glibc dynamically. A static module
// is not: Tarantool, which loads it, has its C library, and the build only
// checks that the module needs no other library. A build for the host takes
// no --target otherwise, so that the project's own configuration of its host
// builds applies; a build for another platform names the gnu or apple target
// of that platform.
func (b cargoBuild) target(platform build.Target, program bool) (string, error) {
	goos, goarch := platform.OS, platform.Arch

	arch, ok := cargoArches[goarch]
	if !ok {
		return "", fmt.Errorf("%w %s-%s", errUnsupportedTarget, goos, goarch)
	}

	host := goos == runtime.GOOS && goarch == runtime.GOARCH

	switch goos {
	case "linux":
		if b.static && program {
			return arch + "-unknown-linux-musl", nil
		}

		if host {
			return "", nil
		}

		return arch + "-unknown-linux-gnu", nil
	case darwinOS:
		if host {
			return "", nil
		}

		return arch + "-apple-darwin", nil
	default:
		return "", fmt.Errorf("%w %s-%s", errUnsupportedTarget, goos, goarch)
	}
}

// message is the part of a cargo JSON message the backend reads: a
// compiler-artifact message names the target it built and, for a binary, the
// executable it produced, and the files it produced.
type message struct {
	Reason string `json:"reason"`
	Target struct {
		Kind []string `json:"kind"`
		Name string   `json:"name"`
	} `json:"target"`
	Executable string   `json:"executable"`
	Filenames  []string `json:"filenames"`
}

// scanMessages calls visit with each JSON message of stdout. A line that is
// not a JSON message - a build script may print to stdout - is skipped.
func scanMessages(stdout []byte, visit func(msg message)) error {
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(nil, len(stdout)+1)

	for scanner.Scan() {
		var msg message

		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}

		visit(msg)
	}

	err := scanner.Err()
	if err != nil {
		return fmt.Errorf("reading the cargo messages: %w", err)
	}

	return nil
}

// findLibrary finds the cdylib cargo built among its JSON messages, and the
// name of its library. cargo reports a package after its dependencies, so the
// last cdylib is that of the package, should a dependency build one too.
func findLibrary(stdout []byte) (string, string, error) {
	library, name := "", ""

	err := scanMessages(stdout, func(msg message) {
		if msg.Reason != "compiler-artifact" || !slices.Contains(msg.Target.Kind, "cdylib") {
			return
		}

		for _, file := range msg.Filenames {
			ext := filepath.Ext(file)
			if ext == linuxModuleExt || ext == darwinModuleExt {
				library, name = file, msg.Target.Name
			}
		}
	})
	if err != nil {
		return "", "", err
	}

	if library == "" {
		return "", "", fmt.Errorf("%w for the library of the package", errNoArtifact)
	}

	return library, name, nil
}

// findExecutable finds the executable of the binary bin among the JSON
// messages cargo printed.
func findExecutable(stdout []byte, bin string) (string, error) {
	executable := ""

	err := scanMessages(stdout, func(msg message) {
		if msg.Reason == "compiler-artifact" && msg.Executable != "" &&
			msg.Target.Name == bin && slices.Contains(msg.Target.Kind, "bin") {
			executable = msg.Executable
		}
	})
	if err != nil {
		return "", err
	}

	if executable == "" {
		return "", fmt.Errorf("%w for the binary %q", errNoArtifact, bin)
	}

	return executable, nil
}
