// Package backend runs a single component build backend and places the
// resulting artifacts in TT_OUTPUT_DIR.
//
// A component with a native part (or a generation step) is built by one of
// four isolated executors selected by the [components.<name>.build] backend
// field: shell (argv-style execve), make (make -C -f target), and a shared cc
// driver serving both c and lua-c. Each executor receives the parsed
// manifest.Build block, an absolute working directory, and the Env contract.
// None of them order components, lay out install namespaces, resolve the lock,
// or run lifecycle hooks — that orchestration is the caller's job (RFC 0010
// task 06); a backend only runs one tool and writes into TT_OUTPUT_DIR.
//
// The process environment is assembled on the exec.Cmd, never via os.Chdir or
// os.Setenv: the seven TT_* contract variables are applied last so a stray
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

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/util"
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
)

// dirPerm is the mode used when creating output directories.
const dirPerm os.FileMode = 0o750

// ErrMissingHeaders reports that the Tarantool development headers are not
// configured (flags.LuaIncDir is empty), so a c / lua-c component cannot be
// compiled. The cc driver returns it before invoking the compiler, mirroring
// the go-luarocks builtin backend's fail-fast on missing headers.
var ErrMissingHeaders = errors.New(
	"tarantool development headers not found: cannot compile c/lua-c component")

// Env is the guaranteed environment contract handed to every backend. The
// caller (RFC 0010 task 06) fully resolves OutputDir — including the install
// namespace — before calling; the backend is namespace-agnostic and only ever
// writes into OutputDir, creating it if absent.
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

// environ builds the child process environment: os.Environ() first, then the
// [build].env pairs in sorted key order, then the seven TT_* contract
// variables last. exec.Cmd resolves duplicate keys last-wins, so the contract
// variables are authoritative — a [build].env key cannot shadow TT_OUTPUT_DIR.
// This is the only place the process environment is assembled; a backend never
// re-reads manifest.Build.Env.
func (e Env) environ() []string {
	out := os.Environ()

	keys := make([]string, 0, len(e.Extra))
	for k := range e.Extra {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		out = append(out, k+"="+e.Extra[k])
	}

	return append(out,
		"TT_OUTPUT_DIR="+e.OutputDir,
		"TT_PROJECT_ROOT="+e.ProjectRoot,
		"TT_PACKAGE="+e.Package,
		"TT_COMPONENT_NAME="+e.Component,
		"TT_VERSION="+e.Version,
		"TT_PLATFORM_OS="+e.platformOS(),
		"TT_PLATFORM_ARCH="+e.platformArch(),
	)
}

// Backend builds one component into env.OutputDir.
type Backend interface {
	// Run builds b in cwd (an absolute path) with the env contract, placing
	// artifacts in env.OutputDir. It returns a plain error on failure; mapping
	// that to a process exit code is the caller's concern.
	Run(ctx context.Context, b manifest.Build, cwd string, env Env) error
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
	default:
		return nil, fmt.Errorf("unknown build backend %q", name)
	}
}

// requireAbsPaths asserts the absolute-path contract for cwd and outputDir. A
// relative cwd would be double-applied (make -C <cwd> plus cmd.Dir=cwd) and a
// relative outputDir would diverge between MkdirAll and the child's -o write,
// so both are rejected rather than papered over.
func requireAbsPaths(cwd, outputDir string) error {
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("cwd must be an absolute path, got %q", cwd)
	}

	if !filepath.IsAbs(outputDir) {
		return fmt.Errorf("output directory must be an absolute path, got %q", outputDir)
	}

	return nil
}

// run builds and runs one subprocess with the assembled contract environment.
// It never mutates the tt process cwd or environment: the environment is set on
// the exec.Cmd and util.RunCommand sets cmd.Dir. exec.CommandContext keeps ctx
// cancellation working.
func run(
	ctx context.Context, cwd string, env Env, showOutput bool, name string, args ...string,
) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env.environ()

	return util.RunCommand(cmd, cwd, showOutput)
}
