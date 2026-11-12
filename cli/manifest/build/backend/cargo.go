package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// cargoTool is the program the cargo backend runs.
const cargoTool = "cargo"

// cargoArches maps a GOARCH to the architecture of a Rust target triple.
var cargoArches = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

// cargoBackend builds a Rust binary with cargo build and copies it into
// TT_OUTPUT_DIR under its bin name; for a module component, the library of
// the package, which Tarantool loads as a native module (see runModule).
//
// Where cargo puts the binary depends on the target, the profile and the
// target directory the project or the environment configures, so the path is
// taken from what cargo reports with --message-format, never guessed.
type cargoBackend struct {
	// showOutput streams child output when true.
	showOutput bool
}

// Run builds the binary of build in cwd as
// cargo build --profile <profile> --bin <bin> and copies it to
// $TT_OUTPUT_DIR/<bin>; for a module component, the library (see runModule).
func (c cargoBackend) Run(ctx context.Context, build manifest.Build, cwd string, env Env) error {
	err := requireAbsPaths(cwd, env.OutputDir)
	if err != nil {
		return err
	}

	err = Preflight(build, env)
	if err != nil {
		return err
	}

	if env.Module {
		return c.runModule(ctx, build, cwd, env)
	}

	bin := build.Executables(env.Component)[0]

	target, err := cargoTarget(build, env)
	if err != nil {
		return err
	}

	stdout, err := runCapture(ctx, cwd, cargoEnviron(env), c.showOutput, cargoTool,
		cargoArgs(build, bin, target)...)
	if err != nil {
		if build.StaticValue() {
			return fmt.Errorf("%w; a static build is made for the target %s, "+
				"which rustup target add %s installs", err, target, target)
		}

		return err
	}

	executable, err := cargoExecutable(stdout, bin)
	if err != nil {
		return err
	}

	err = copyFile(executable, filepath.Join(env.OutputDir, bin))
	if err != nil {
		return fmt.Errorf("copy the binary %q: %w", executable, err)
	}

	return nil
}

// runModule builds the library of the package in cwd as a cdylib, which
// Tarantool loads as a native module - with require, and as the module of C
// stored procedures - and copies it to $TT_OUTPUT_DIR/<module><ext>: module
// is the name of the library unless the build names another, and the
// extension .so, .dylib on macOS (see moduleExt).
//
// cargo rustc --crate-type cdylib builds a cdylib whatever crate-type the
// package declares, and passes the link arguments of macOS to the library
// alone: its symbols of Tarantool are left to the loader, -undefined
// dynamic_lookup, as those of a C module are, and the dependencies are built
// as cargo builds them anyway.
func (c cargoBackend) runModule(
	ctx context.Context, build manifest.Build, cwd string, env Env,
) error {
	target, err := cargoTarget(build, env)
	if err != nil {
		return err
	}

	goos := env.platformOS()

	stdout, err := runCapture(ctx, cwd, cargoEnviron(env), c.showOutput, cargoTool,
		cargoModuleArgs(build, target, goos)...)
	if err != nil {
		return err
	}

	library, name, err := cargoLibrary(stdout)
	if err != nil {
		return err
	}

	module := build.Module
	if module == "" {
		module = name
	}

	err = os.MkdirAll(env.OutputDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create output directory %q: %w", env.OutputDir, err)
	}

	err = copyFile(library, filepath.Join(env.OutputDir, module+moduleExt(goos, linuxModuleExt)))
	if err != nil {
		return fmt.Errorf("copy the library %q: %w", library, err)
	}

	return nil
}

// cargoTargetDirKeys are the environment variables that name the target
// directory of cargo: CARGO_TARGET_DIR, and the build.target-dir setting of
// the cargo configuration as an environment variable.
var cargoTargetDirKeys = []string{"CARGO_TARGET_DIR", "CARGO_BUILD_TARGET_DIR"}

// cargoEnviron is the environment of a cargo build: the contract of env, and
// CARGO_TARGET_DIR, env.CargoTargetDir, in the project's _build/, so that the
// build leaves no target/ in the sources - another checkout's, for a path
// dependency. A target directory the environment of the build names, in
// [build].env or the environment tt runs in, is the user's choice and is
// kept; build.target-dir of the cargo configuration of the sources gives way,
// CARGO_TARGET_DIR winning over it. Where the target directory is makes no
// difference to the backend: it takes the artifacts from the paths cargo
// reports.
func cargoEnviron(env Env) []string {
	environ := env.environ()
	if env.CargoTargetDir == "" {
		return environ
	}

	for _, key := range cargoTargetDirKeys {
		if value, ok := lookupEnv(environ, key); ok && value != "" {
			return environ
		}
	}

	return append(environ, "CARGO_TARGET_DIR="+env.CargoTargetDir)
}

// cargoArgs builds the cargo build argv. Diagnostics are rendered by cargo
// to stderr as usual, and only the JSON messages go to stdout.
func cargoArgs(build manifest.Build, bin, target string) []string {
	return append([]string{
		"build", "--profile", cargoProfile(build), "--bin", bin,
		"--message-format=json-render-diagnostics",
	}, cargoSelection(build, target)...)
}

// cargoModuleArgs builds the cargo rustc argv of the library of a module
// component, for goos.
func cargoModuleArgs(build manifest.Build, target, goos string) []string {
	args := []string{
		"rustc", "--lib", "--crate-type", "cdylib", "--profile", cargoProfile(build),
		"--message-format=json-render-diagnostics",
	}

	args = append(args, cargoSelection(build, target)...)

	if goos == darwinOS {
		args = append(args, "--", "-C", "link-arg=-undefined", "-C", "link-arg=dynamic_lookup")
	}

	return args
}

// cargoProfile is the profile of build, release unless it names another.
func cargoProfile(build manifest.Build) string {
	if build.Profile == "" {
		return manifest.DefaultCargoProfile
	}

	return build.Profile
}

// cargoSelection is the part of a cargo argv that selects what to build: the
// package, the features and the target.
func cargoSelection(build manifest.Build, target string) []string {
	var args []string

	if build.Package != "" {
		args = append(args, "--package", build.Package)
	}

	if len(build.Features) > 0 {
		args = append(args, "--features", strings.Join(build.Features, ","))
	}

	if target != "" {
		args = append(args, "--target", target)
	}

	return args
}

// cargoTarget picks the --target of a build for the build platform, or none
// when cargo's own host target does.
//
// A static linux program is built for <arch>-unknown-linux-musl, which links
// the C library in: the gnu targets link glibc dynamically. A static module
// is not: Tarantool, which loads it, has its C library, and the build only
// checks that the module needs no other library. A build for the host takes
// no --target otherwise, so that the project's own configuration of its host
// builds applies; a build for another platform names the gnu or apple target
// of that platform.
func cargoTarget(build manifest.Build, env Env) (string, error) {
	goos, goarch := env.platformOS(), env.platformArch()

	arch, ok := cargoArches[goarch]
	if !ok {
		return "", fmt.Errorf("%w %s-%s", errUnsupportedTarget, goos, goarch)
	}

	host := goos == runtime.GOOS && goarch == runtime.GOARCH

	switch goos {
	case "linux":
		if build.StaticValue() && !env.Module {
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

// cargoMessage is the part of a cargo JSON message the backend reads: a
// compiler-artifact message names the target it built and, for a binary, the
// executable it produced, and the files it produced.
type cargoMessage struct {
	Reason string `json:"reason"`
	Target struct {
		Kind []string `json:"kind"`
		Name string   `json:"name"`
	} `json:"target"`
	Executable string   `json:"executable"`
	Filenames  []string `json:"filenames"`
}

// cargoLibrary finds the cdylib cargo built among its JSON messages, and the
// name of its library. cargo reports a package after its dependencies, so the
// last cdylib is that of the package, should a dependency build one too.
func cargoLibrary(stdout []byte) (string, string, error) {
	library, name := "", ""

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(nil, len(stdout)+1)

	for scanner.Scan() {
		var msg cargoMessage

		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}

		if msg.Reason != "compiler-artifact" || !slices.Contains(msg.Target.Kind, "cdylib") {
			continue
		}

		for _, file := range msg.Filenames {
			ext := filepath.Ext(file)
			if ext == linuxModuleExt || ext == darwinModuleExt {
				library, name = file, msg.Target.Name
			}
		}
	}

	err := scanner.Err()
	if err != nil {
		return "", "", fmt.Errorf("reading the cargo messages: %w", err)
	}

	if library == "" {
		return "", "", fmt.Errorf("%w for the library of the package", errNoArtifact)
	}

	return library, name, nil
}

// cargoExecutable finds the executable of the binary bin among the JSON
// messages cargo printed. A line that is not a JSON message - a build script
// may print to stdout - is skipped.
func cargoExecutable(stdout []byte, bin string) (string, error) {
	executable := ""

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(nil, len(stdout)+1)

	for scanner.Scan() {
		var msg cargoMessage

		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}

		if msg.Reason == "compiler-artifact" && msg.Executable != "" &&
			msg.Target.Name == bin && slices.Contains(msg.Target.Kind, "bin") {
			executable = msg.Executable
		}
	}

	err := scanner.Err()
	if err != nil {
		return "", fmt.Errorf("reading the cargo messages: %w", err)
	}

	if executable == "" {
		return "", fmt.Errorf("%w for the binary %q", errNoArtifact, bin)
	}

	return executable, nil
}

// runCapture runs a subprocess like runEnviron, but returns its stdout: the
// cargo backend reads its JSON messages from there. The rest follows
// util.RunCommand: stderr is streamed with showOutput, and otherwise kept and
// shown only when the command fails, with a spinner while it runs.
func runCapture(
	ctx context.Context, cwd string, environ []string,
	showOutput bool, name string, args ...string,
) ([]byte, error) {
	cmd := exec.CommandContext(ctx, lookPathIn(name, environ), args...)

	cmd.Args[0] = name
	cmd.Env = environ
	cmd.Dir = cwd

	logCommand(cmd, cwd)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	stopSpinner := func() {}

	if showOutput {
		cmd.Stderr = os.Stderr
	} else {
		stopSpinner = log.Spinner("running " + strings.Join(cmd.Args, " "))
	}

	err := cmd.Run()

	stopSpinner()

	if err != nil {
		_, _ = os.Stderr.Write(stderr.Bytes())

		return nil, fmt.Errorf("failed to run \n%s\n\n%w", cmd.String(), err)
	}

	return stdout.Bytes(), nil
}
