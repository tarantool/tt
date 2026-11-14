// Package buildtest runs a build backend in tests as the core runs it: it
// checks a build block for it ([Validate]) and runs its build in a
// [Context] on temporary directories ([New], [Harness.Run]).
//
// The checks are those the core makes of a block in a manifest, but for a
// tt that has no other backend: a field the configuration of the backend has
// no place for is an unknown one here, where the core may say that another
// backend has it.
package buildtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tarantool/tt/sdk/build"
)

// Block is a build block as a test writes it: the common fields, and the
// fields of the backend as a TOML decoder makes them of the block - strings,
// int64, float64, bool, []any and map[string]any.
type Block struct {
	// Common are the common fields of the block but incremental.
	Common build.Common
	// Incremental is incremental = true.
	Incremental bool
	// Rest are the fields of the backend.
	Rest map[string]any
}

// Validate checks block, the build block of component, for backend as the
// core checks one, in its order: the fields of the backend are decoded into
// its configuration ([build.Backend.Decode]), a field it has no place for
// being unknown; backend.New checks them; then output, incremental and the
// kind of component are checked against what backend allows. It returns the
// build, or the first error: a [*build.FieldError] for a field, with its
// path from the block.
func Validate(backend build.Backend, component build.Component, block Block) (build.Build, error) {
	cfg, unknown, err := backend.Decode(block.Rest)
	if err != nil {
		return nil, err
	}

	if len(unknown) > 0 {
		return nil, build.FieldErrorf(unknown[0], "unknown field")
	}

	if backend.New == nil {
		return nil, fmt.Errorf("%w: backend %q has no New", build.ErrConfig, backend.Name)
	}

	bld, err := backend.New(cfg, component, block.Common)
	if err != nil {
		return nil, err
	}

	switch {
	case block.Common.Output != nil && backend.OutputFrom == build.OutputNone:
		return nil, build.FieldErrorf("output", "is not a field of backend %q", backend.Name)
	case block.Incremental && !backend.Incremental:
		return nil, build.FieldErrorf("incremental", "is not a field of backend %q",
			backend.Name)
	case !slices.Contains(backend.Kinds, component.Kind):
		return nil, build.FieldErrorf("", "backend %q does not build a component of kind %q",
			backend.Name, component.Kind)
	}

	return bld, nil
}

// dirPerm is the mode of the directories of a harness.
const dirPerm = 0o750

// Harness is a Context of a build on temporary directories, whose commands
// run on the machine the test runs on.
type Harness struct {
	// Context is the Context the build runs in. A test may change its facts
	// before it runs the build.
	Context *build.Context
	// Locked are the versions of the rocks the lock pins, as
	// [build.Context.LockedVersion] answers.
	Locked map[string]string

	mu       sync.Mutex
	printed  bytes.Buffer
	warnings []string
}

// New returns a Harness for the build of component, of the package demo
// unless it names another, in directories of tb.TempDir(): <tmp>/project is
// the project, with the component at its Path in it (its name when Path is
// empty), its .tarantool/bin and .rocks; <tmp>/work, <tmp>/output,
// <tmp>/share and <tmp>/state are the other directories of the build. All of
// them but the tree exist. The target is the machine the test runs on, the
// toolchain is cc of CC with the flags of the target, and no Tarantool is
// known.
func New(tb testing.TB, component build.Component) *Harness {
	tb.Helper()

	root := tb.TempDir()
	project := filepath.Join(root, "project")

	if component.Package == "" {
		component.Package = "demo"
	}

	path := component.Path
	if path == "" {
		path = component.Name
	}

	dirs := build.Dirs{
		Project:   project,
		Component: filepath.Join(project, path),
		Cwd:       filepath.Join(project, path),
		Work:      filepath.Join(root, "work"),
		Output:    filepath.Join(root, "output"),
		Share:     filepath.Join(root, "share"),
		State:     filepath.Join(root, "state"),
		Bin:       filepath.Join(project, ".tarantool", "bin"),
		Tree:      filepath.Join(project, ".rocks"),
	}

	for _, dir := range []string{dirs.Component, dirs.Work, dirs.Output, dirs.Share, dirs.State,
		dirs.Bin} {
		err := os.MkdirAll(dir, dirPerm)
		if err != nil {
			tb.Fatalf("buildtest: %v", err)
		}
	}

	target := build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}

	harness := &Harness{
		Context:  nil,
		Locked:   map[string]string{},
		mu:       sync.Mutex{},
		printed:  bytes.Buffer{},
		warnings: nil,
	}

	harness.Context = build.NewContext(build.Context{
		Component: component,
		Target:    target,
		Dirs:      dirs,
		Tarantool: build.Tarantool{Prefix: "", IncludeDir: "", Version: ""},
		Toolchain: toolchain(target),
		Env: map[string]string{
			"TT_OUTPUT_DIR":     dirs.Output,
			"TT_PROJECT_ROOT":   dirs.Project,
			"TT_PACKAGE":        component.Package,
			"TT_COMPONENT_NAME": component.Name,
			"TT_PLATFORM_OS":    target.OS,
			"TT_PLATFORM_ARCH":  target.Arch,
			"TT_BUILD_DIR":      dirs.Work,
		},
		Fresh:  false,
		Locked: false,
		Static: false,
		Log:    slog.New(slog.DiscardHandler),
		Warn:   harness.warn,
	}, host{h: harness})

	return harness
}

// toolchain is how tt compiles a C module for target.
func toolchain(target build.Target) build.Toolchain {
	compiler := os.Getenv("CC")
	if compiler == "" {
		compiler = "cc"
	}

	libFlag := []string{"-shared"}
	if target.OS == "darwin" {
		libFlag = []string{"-bundle", "-undefined", "dynamic_lookup"}
	}

	return build.Toolchain{
		CC: compiler, LibFlag: libFlag, Rpath: target.OS != "darwin", IncludeDir: "",
	}
}

// Run runs bld as the core runs a build: its Preflight first, when it has
// one, then Run.
func (h *Harness) Run(ctx context.Context, bld build.Build) error {
	if preflighter, ok := bld.(build.Preflighter); ok {
		err := preflighter.Preflight(h.Context)
		if err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
	}

	return bld.Run(ctx, h.Context)
}

// Printed returns what the commands of the build printed so far, but the
// stdout [build.Context.Output] returned.
func (h *Harness) Printed() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.printed.String()
}

// Warnings returns the warnings the build showed so far.
func (h *Harness) Warnings() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.warnings)
}

// warn records a warning of the build.
func (h *Harness) warn(warning string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.warnings = append(h.warnings, warning)
}

// writer is where the commands of the build print to.
func (h *Harness) writer() io.Writer {
	return lockedWriter{h: h}
}

// lockedWriter writes into what the harness printed, under its lock.
type lockedWriter struct {
	h *Harness
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.h.mu.Lock()
	defer w.h.mu.Unlock()

	n, err := w.h.printed.Write(p)
	if err != nil {
		return n, fmt.Errorf("buildtest: %w", err)
	}

	return n, nil
}

// host runs the commands of the build of a harness.
type host struct {
	h *Harness
}

func (s host) Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	environ := s.environ()

	program, err := lookPath(name, environ)
	if err != nil {
		// Running it fails as running a program that is not there does.
		program = name
	}

	cmd := exec.CommandContext(ctx, program, args...)

	cmd.Args[0] = name
	cmd.Dir = s.h.Context.Dirs.Cwd
	cmd.Env = environ

	return cmd
}

func (s host) Run(cmd *exec.Cmd) error {
	if cmd.Stdout == nil {
		cmd.Stdout = s.h.writer()
	}

	if cmd.Stderr == nil {
		cmd.Stderr = s.h.writer()
	}

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("running %s: %w", strings.Join(cmd.Args, " "), err)
	}

	return nil
}

func (s host) Output(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stderr == nil {
		cmd.Stderr = s.h.writer()
	}

	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("running %s: %w", strings.Join(cmd.Args, " "), err)
	}

	return out, nil
}

func (s host) LookPath(name string) (string, error) {
	return lookPath(name, s.environ())
}

func (s host) LockedVersion(rock string) (string, bool) {
	version, ok := s.h.Locked[rock]

	return version, ok
}

// environ is the environment of the commands: that of the test, the
// variables of the Context in key order, and the bin directory of the
// project first in PATH.
func (s host) environ() []string {
	facts := s.h.Context
	environ := os.Environ()

	keys := make([]string, 0, len(facts.Env))
	for key := range facts.Env {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	path := os.Getenv("PATH")

	for _, key := range keys {
		environ = append(environ, key+"="+facts.Env[key])

		if key == "PATH" {
			path = facts.Env[key]
		}
	}

	return append(environ, "PATH="+facts.Dirs.Bin+string(os.PathListSeparator)+path)
}

// errNotFound reports a program that is not in the PATH of the build.
var errNotFound = errors.New("not found in PATH")

// lookPath finds name in the last PATH of environ; a name with a slash is
// taken as it is.
func lookPath(name string, environ []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}

	var path string

	for _, entry := range environ {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			path = value
		}
	}

	for _, dir := range filepath.SplitList(path) {
		candidate := filepath.Join(dir, name)

		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("%s: %w", name, errNotFound)
}
