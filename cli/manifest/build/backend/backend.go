// Package backend runs a single component build backend and places the
// resulting artifacts in TT_OUTPUT_DIR.
//
// A component with a native part (or a generation step) is built by one of
// the isolated executors selected by the [components.<name>.build] backend
// field: shell (argv-style execve), make (make -C -f target), a shared cc
// driver serving both c and lua-c, cmake (configure and build out of the
// source), and, for an executable component, go (go build) and cargo (cargo
// build). Each executor receives the parsed manifest.Build block, an absolute
// working directory, and the Env contract. None of them order components, lay
// out install namespaces, resolve the lock, or run lifecycle hooks — that
// orchestration is the caller's job; a backend only runs one tool and writes
// into TT_OUTPUT_DIR.
//
// The process environment is assembled on the exec.Cmd, never via os.Chdir or
// os.Setenv: the TT_* contract variables are applied last so a stray
// [build].env pair cannot redirect e.g. TT_OUTPUT_DIR. The cc driver reuses
// go-luarocks' per-OS flag derivation through the rocks adapter's Flags seam,
// so -fPIC / -I<include> / -shared-vs-bundle / .so are not duplicated here.
package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"

	lrbuild "github.com/tarantool/go-luarocks/build"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/util"
)

// Backend build names. They mirror the closed enum validated at manifest parse
// time (cli/manifest/validate.go); manifest keeps its own copies unexported, so
// the dispatch vocabulary is redeclared here.
const (
	// BackendShell runs an arbitrary command argv-style (no shell parsing).
	BackendShell = "shell"
	// BackendMake drives make against a component-supplied Makefile.
	BackendMake = "make"
	// BackendC compiles a stored-proc C module (loaded via box.schema.func).
	BackendC = "c"
	// BackendLuaC compiles a Lua C extension (exports luaopen_<module>).
	BackendLuaC = "lua-c"
	// BackendGo builds a Go program with go build.
	BackendGo = "go"
	// BackendCargo builds a Rust binary with cargo build.
	BackendCargo = "cargo"
	// BackendCMake configures and builds a CMake project out of its source.
	BackendCMake = "cmake"
)

// dirPerm is the mode used when creating output directories.
const dirPerm os.FileMode = 0o750

// ErrMissingHeaders reports that the Tarantool development headers are not
// configured (flags.LuaIncDir is empty), so a c / lua-c component cannot be
// compiled. The cc driver returns it before invoking the compiler, mirroring
// the go-luarocks builtin backend's fail-fast on missing headers.
var ErrMissingHeaders = errors.New(
	"tarantool development headers not found: cannot compile c/lua-c component")

var (
	// errUnknownBackend reports a build backend name New does not know.
	errUnknownBackend = errors.New("unknown build backend")
	// errRelativeOutputDir reports an output directory that is not absolute.
	errRelativeOutputDir = errors.New("output directory must be an absolute path")
)

// Env is the guaranteed environment contract handed to every backend. The
// caller fully resolves OutputDir — including the install namespace — before
// calling; the backend is namespace-agnostic and only ever writes into
// OutputDir, creating it if absent.
type Env struct {
	// OutputDir is where native artifacts are placed (TT_OUTPUT_DIR). It must
	// be an absolute path.
	OutputDir string
	// ProjectRoot is the package root (TT_PROJECT_ROOT).
	ProjectRoot string
	// Package is the package name (TT_PACKAGE).
	Package string
	// Component is the component name (TT_COMPONENT_NAME).
	Component string
	// Version is the package version (TT_VERSION).
	Version string
	// OS is the target platform OS (TT_PLATFORM_OS); empty defaults to
	// runtime.GOOS, which also selects the [build].platforms.<os> overlay.
	OS string
	// Arch is the target platform architecture (TT_PLATFORM_ARCH); empty
	// defaults to runtime.GOARCH.
	Arch string
	// Extra carries the [build].env pairs supplied by the manifest.
	Extra map[string]string
	// Module is set for the build of a module component: the cargo backend
	// builds the library of the package then, and the binary otherwise.
	Module bool
	// TarantoolPrefix is the install prefix of the Tarantool the build is
	// for (TT_TARANTOOL_PREFIX); empty when tt does not know it. The cmake
	// backend puts it into CMAKE_PREFIX_PATH.
	TarantoolPrefix string
	// TarantoolIncludeDir is the directory of the C headers of that
	// Tarantool, module.h and lua.h, as #include <module.h> finds them
	// (TT_TARANTOOL_INCLUDE_DIR); empty when there are none.
	TarantoolIncludeDir string
	// BuildDir is the directory of the project's _build/ the build of the
	// component keeps its intermediate files in, between builds too
	// (TT_BUILD_DIR): the build tree of a cmake build, what a make or shell
	// build makes on the way, out of its sources. It must be absolute for a
	// backend that uses it; empty sets no TT_BUILD_DIR.
	BuildDir string
	// Fresh asks the backend to drop what it keeps in BuildDir and to build
	// from scratch, as tt package build --rebuild does.
	Fresh bool
	// CargoTargetDir is the target directory of a cargo build
	// (CARGO_TARGET_DIR), in the project's _build/, unless the environment
	// of the build names one; empty leaves cargo its own.
	CargoTargetDir string
}

// platformOS returns env.OS, defaulting to the host runtime.GOOS when empty so
// an unset field never silently disables the per-OS overlay.
func (e Env) platformOS() string {
	if e.OS == "" {
		return runtime.GOOS
	}

	return e.OS
}

// platformArch returns env.Arch, defaulting to the host runtime.GOARCH.
func (e Env) platformArch() string {
	if e.Arch == "" {
		return runtime.GOARCH
	}

	return e.Arch
}

// baseEnviron builds the shared prefix of every child environment: os.Environ()
// first, then the [build].env pairs in sorted key order. The TT_* contract
// variables are appended last by environ / hookEnviron so exec.Cmd's last-wins
// resolution keeps them authoritative — a [build].env key cannot shadow
// TT_OUTPUT_DIR. This is the only place the process environment is assembled; a
// backend never re-reads manifest.Build.Env.
func (e Env) baseEnviron() []string {
	out := os.Environ()

	keys := make([]string, 0, len(e.Extra))
	for k := range e.Extra {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		out = append(out, k+"="+e.Extra[k])
	}

	return withProjectBin(out, e.ProjectRoot)
}

// environ is the full component-build contract: baseEnviron plus the seven TT_*
// variables, TT_OUTPUT_DIR / TT_COMPONENT_NAME included, TT_BUILD_DIR, and
// those of Tarantool (see tarantoolEnviron).
func (e Env) environ() []string {
	environ := append(e.baseEnviron(),
		"TT_OUTPUT_DIR="+e.OutputDir,
		"TT_PROJECT_ROOT="+e.ProjectRoot,
		"TT_PACKAGE="+e.Package,
		"TT_COMPONENT_NAME="+e.Component,
		"TT_VERSION="+e.Version,
		"TT_PLATFORM_OS="+e.platformOS(),
		"TT_PLATFORM_ARCH="+e.platformArch(),
	)

	if e.BuildDir != "" {
		environ = append(environ, "TT_BUILD_DIR="+e.BuildDir)
	}

	return e.tarantoolEnviron(environ)
}

// hookEnviron is the reduced lifecycle-hook contract: baseEnviron plus the five
// TT_* variables that exist before any component is built, and those of
// Tarantool. TT_OUTPUT_DIR, TT_COMPONENT_NAME and TT_BUILD_DIR are
// deliberately omitted — a pre_build / post_build hook runs around all
// components and has no single output directory, component name or
// directory of intermediate files.
func (e Env) hookEnviron() []string {
	return e.tarantoolEnviron(append(e.baseEnviron(),
		"TT_PROJECT_ROOT="+e.ProjectRoot,
		"TT_PACKAGE="+e.Package,
		"TT_VERSION="+e.Version,
		"TT_PLATFORM_OS="+e.platformOS(),
		"TT_PLATFORM_ARCH="+e.platformArch(),
	))
}

// tarantoolEnviron appends to environ where the Tarantool the build is for
// is: TT_TARANTOOL_PREFIX, its install prefix, and TT_TARANTOOL_INCLUDE_DIR,
// the directory of its module.h and lua.h, which a make, shell or cmake build
// of a C module compiles against as the c and lua-c backends do. Each is set
// only when tt knows it: a guessed path would send the build to look for the
// headers where they are not, rather than let it fail or find them itself.
func (e Env) tarantoolEnviron(environ []string) []string {
	if e.TarantoolPrefix != "" {
		environ = append(environ, "TT_TARANTOOL_PREFIX="+e.TarantoolPrefix)
	}

	if e.TarantoolIncludeDir != "" {
		environ = append(environ, "TT_TARANTOOL_INCLUDE_DIR="+e.TarantoolIncludeDir)
	}

	return environ
}

// Backend builds one component into env.OutputDir.
type Backend interface {
	// Run builds build in cwd (an absolute path) with the env contract, placing
	// artifacts in env.OutputDir. It returns a plain error on failure; mapping
	// that to a process exit code is the caller's concern.
	Run(ctx context.Context, build manifest.Build, cwd string, env Env) error
}

// New returns the Backend for the named build backend. flags supplies the
// compile/link toolchain and is consumed only by c / lua-c; showOutput streams
// child stdout/stderr when true, otherwise output is buffered and printed only
// on failure. c and lua-c share one cc driver — the driver is identical and
// only the later load path differs. An unknown name is an error.
func New(name string, flags lrbuild.Flags, showOutput bool) (Backend, error) {
	switch name {
	case BackendShell:
		return shellBackend{showOutput: showOutput}, nil
	case BackendMake:
		return makeBackend{showOutput: showOutput}, nil
	case BackendC, BackendLuaC:
		return ccBackend{flags: flags, showOutput: showOutput}, nil
	case BackendGo:
		return goBackend{showOutput: showOutput}, nil
	case BackendCargo:
		return cargoBackend{showOutput: showOutput}, nil
	case BackendCMake:
		return cmakeBackend{showOutput: showOutput}, nil
	default:
		return nil, fmt.Errorf("%w %q", errUnknownBackend, name)
	}
}

// requireAbsPaths asserts the absolute-path contract for cwd and outputDir. A
// relative cwd would be double-applied (make -C <cwd> plus cmd.Dir=cwd) and a
// relative outputDir would diverge between MkdirAll and the child's -o write,
// so both are rejected rather than papered over.
func requireAbsPaths(cwd, outputDir string) error {
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("%w, got %q", errRelativeCwd, cwd)
	}

	if !filepath.IsAbs(outputDir) {
		return fmt.Errorf("%w, got %q", errRelativeOutputDir, outputDir)
	}

	return nil
}

// run builds and runs one subprocess with the full component-build contract
// environment. It never mutates the tt process cwd or environment: the
// environment is set on the exec.Cmd and util.RunCommand sets cmd.Dir.
// exec.CommandContext keeps ctx cancellation working.
func run(
	ctx context.Context, cwd string, env Env, showOutput bool, name string, args ...string,
) error {
	return runEnviron(ctx, cwd, env.environ(), showOutput, name, args...)
}

// runEnviron is the shared subprocess launcher: it takes the fully assembled
// environment slice so the component path (run) and the hook path (RunHook) can
// supply their respective contracts without duplicating exec wiring.
func runEnviron(
	ctx context.Context, cwd string, environ []string,
	showOutput bool, name string, args ...string,
) error {
	cmd := exec.CommandContext(ctx, lookPathIn(name, environ), args...)

	cmd.Args[0] = name
	cmd.Env = environ

	return util.RunCommand(cmd, cwd, showOutput)
}
