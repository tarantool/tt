// Package buildtest runs a build backend in tests as the core runs it: it
// checks a build block for it ([Validate], [ValidateHook]) and runs its
// build in a [build.Context] on temporary directories ([New], [NewHook],
// [Harness.Run]).
//
// The checks are those the core makes, with the same functions of
// package build: of the backend before the core adds it
// ([build.Backend.Validate]), of each build it makes
// ([build.Backend.ValidateBuild]), and of a block in a manifest, but for a
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
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
// core checks one, in its order: backend is one the core takes
// ([build.Backend.Validate]); the fields of the backend are decoded into
// its configuration ([build.Backend.Decode]), a field it has no place for
// being unknown; backend.New checks them, and the build it returns is
// checked ([build.Backend.ValidateBuild]); then output and incremental are
// checked against what backend allows, and the types of what the block
// asks for - the to of each file of output, those the build says it makes
// ([build.Producer]) - and those component expects against what backend
// produces. It returns the build, or the first error: a [*build.FieldError]
// for a field, with its path from the block.
func Validate(backend build.Backend, component build.Component, block Block) (build.Build, error) {
	bld, err := validate(backend, component, block)
	if err != nil {
		return nil, err
	}

	for i, file := range block.Common.Output {
		if !slices.Contains(backend.Produces, file.To) {
			return nil, build.FieldErrorf(fmt.Sprintf("output[%d].to", i),
				"backend %q makes no %s", backend.Name, file.To)
		}
	}

	var asked []build.Result

	if producer, ok := bld.(build.Producer); ok {
		asked = producer.Produces()
	}

	for _, result := range asked {
		if !slices.Contains(backend.Produces, result) {
			return nil, build.FieldErrorf("", "backend %q makes no %s, but the build "+
				"says it does", backend.Name, result)
		}
	}

	if component.Expects == nil {
		return bld, nil
	}

	if !slices.ContainsFunc(component.Expects,
		func(result build.Result) bool { return slices.Contains(backend.Produces, result) }) {
		return nil, build.FieldErrorf("", "backend %q makes nothing the component expects",
			backend.Name)
	}

	for _, result := range asked {
		if !slices.Contains(component.Expects, result) {
			return nil, build.FieldErrorf("", "the build makes a %s, which the component "+
				"does not expect", result)
		}
	}

	return bld, nil
}

// ValidateHook checks block, the block of a lifecycle hook, for backend as
// the core checks one: as Validate does, New being given the zero
// component, and the backend must run hooks ([build.Backend.Hook]); what a
// hook makes is not looked at.
func ValidateHook(backend build.Backend, block Block) (build.Build, error) {
	if !backend.Hook {
		return nil, build.FieldErrorf("", "backend %q runs no hook", backend.Name)
	}

	return validate(backend, build.Component{Name: "", Path: "", Namespace: "", Package: "",
		Expects: nil}, block)
}

// validate is what Validate and ValidateHook check alike: first that
// backend is one the core takes ([build.Backend.Validate]).
func validate(backend build.Backend, component build.Component, block Block) (build.Build, error) {
	err := backend.Validate()
	if err != nil {
		return nil, err
	}

	cfg, unknown, err := backend.Decode(block.Rest)
	if err != nil {
		return nil, err
	}

	if len(unknown) > 0 {
		return nil, build.FieldErrorf(unknown[0], "unknown field")
	}

	bld, err := backend.New(cfg, component, block.Common)
	if err != nil {
		return nil, err
	}

	err = backend.ValidateBuild(bld)
	if err != nil {
		return nil, err
	}

	switch {
	case block.Common.Output != nil && backend.OutputFrom == build.OutputNone:
		return nil, build.FieldErrorf("output", "is not a field of backend %q", backend.Name)
	case block.Incremental && !backend.Incremental:
		return nil, build.FieldErrorf("incremental", "is not a field of backend %q",
			backend.Name)
	}

	return bld, nil
}

// dirPerm is the mode of the directories of a harness.
const dirPerm = 0o750

// errNoHook reports a harness of hooks of a backend that runs none.
var errNoHook = errors.New("the backend runs no hook")

// Harness is a Context of a build on temporary directories, whose commands
// run on the machine the test runs on.
type Harness struct {
	// Context is the Context the build runs in. A test may change its facts
	// before it runs the build.
	Context *build.Context
	// Locked are the versions of the rocks the lock pins, as
	// [build.Context.LockedVersion] answers.
	Locked map[string]string

	// backend is the backend whose builds the harness runs.
	backend build.Backend

	mu       sync.Mutex
	printed  bytes.Buffer
	warnings []string
}

// New returns a Harness for the builds backend makes of component, of the
// package demo unless it names another, in directories of tb.TempDir():
// <tmp>/project is the project, with the component at its Path in it (its
// name when Path is empty), its .tarantool/bin and .rocks; <tmp>/work,
// <tmp>/output, <tmp>/share, <tmp>/bin and <tmp>/state are the other
// directories of the build. All of them but the tree exist. The target is
// the machine the test runs on, the toolchain is cc of CC with the flags of
// the target, and no Tarantool is known. A backend the core would refuse
// ([build.Backend.Validate]) fails the test.
func New(tb testing.TB, backend build.Backend, component build.Component) *Harness {
	tb.Helper()

	err := backend.Validate()
	if err != nil {
		tb.Fatalf("buildtest: %v", err)
	}

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
		Programs:  filepath.Join(root, "bin"),
		State:     filepath.Join(root, "state"),
		Tools:     filepath.Join(project, ".tarantool", "bin"),
		Tree:      filepath.Join(project, ".rocks"),
	}

	for _, dir := range []string{dirs.Component, dirs.Work, dirs.Output, dirs.Share,
		dirs.Programs, dirs.State, dirs.Tools} {
		err := os.MkdirAll(dir, dirPerm)
		if err != nil {
			tb.Fatalf("buildtest: %v", err)
		}
	}

	target := build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}

	return newHarness(backend, build.Context{
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
		Hook:   false,
		Log:    nil,
		Warn:   nil,
	})
}

// NewHook returns a Harness for the builds backend makes of a lifecycle
// hook of the package demo, in a Context whose Hook is set
// ([build.Context.Hook]): in directories of tb.TempDir(), <tmp>/project is
// the project, the working directory of the hook, with its .tarantool/bin
// and .rocks, and <tmp>/state the state of its build; there is no
// component, no work directory and no output, and the environment has no
// TT_OUTPUT_DIR, TT_COMPONENT_NAME and TT_BUILD_DIR. A backend the core
// would refuse, or one that runs no hook ([build.Backend.Hook]), fails the
// test.
func NewHook(tb testing.TB, backend build.Backend) *Harness {
	tb.Helper()

	err := backend.Validate()
	if err == nil && !backend.Hook {
		err = fmt.Errorf("%w: %q", errNoHook, backend.Name)
	}

	if err != nil {
		tb.Fatalf("buildtest: %v", err)
	}

	root := tb.TempDir()
	project := filepath.Join(root, "project")

	dirs := build.Dirs{
		Project:   project,
		Component: "",
		Cwd:       project,
		Work:      "",
		Output:    "",
		Share:     "",
		Programs:  "",
		State:     filepath.Join(root, "state"),
		Tools:     filepath.Join(project, ".tarantool", "bin"),
		Tree:      filepath.Join(project, ".rocks"),
	}

	for _, dir := range []string{dirs.Project, dirs.State, dirs.Tools} {
		err := os.MkdirAll(dir, dirPerm)
		if err != nil {
			tb.Fatalf("buildtest: %v", err)
		}
	}

	target := build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}

	return newHarness(backend, build.Context{
		Component: build.Component{Name: "", Path: "", Namespace: "", Package: "", Expects: nil},
		Target:    target,
		Dirs:      dirs,
		Tarantool: build.Tarantool{Prefix: "", IncludeDir: "", Version: ""},
		Toolchain: toolchain(target),
		Env: map[string]string{
			"TT_PROJECT_ROOT":  dirs.Project,
			"TT_PACKAGE":       "demo",
			"TT_PLATFORM_OS":   target.OS,
			"TT_PLATFORM_ARCH": target.Arch,
		},
		Fresh:  false,
		Locked: false,
		Static: false,
		Hook:   true,
		Log:    nil,
		Warn:   nil,
	})
}

// newHarness returns a Harness of the builds of backend whose Context has
// the facts of facts, its log discarded and its warnings recorded.
func newHarness(backend build.Backend, facts build.Context) *Harness {
	harness := &Harness{
		Context:  nil,
		Locked:   map[string]string{},
		backend:  backend,
		mu:       sync.Mutex{},
		printed:  bytes.Buffer{},
		warnings: nil,
	}

	facts.Log = slog.New(slog.DiscardHandler)
	facts.Warn = harness.warn
	harness.Context = build.NewContext(facts, host{h: harness})

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

// Run runs bld, a build of the backend of the harness, as the core runs a
// build: bld is checked ([build.Backend.ValidateBuild]), then its Preflight
// runs, when it has one, then Run; then what Run left in the directories
// of the types the backend does not produce fails the run, wrapping
// [build.ErrNotProduced], as it fails a build of the core, and so does a
// program a [build.ProgramNamer] names. The files output lists are not
// copied, which the core does for [build.OutputCwd].
func (h *Harness) Run(ctx context.Context, bld build.Build) error {
	err := h.backend.ValidateBuild(bld)
	if err != nil {
		return err
	}

	if preflighter, ok := bld.(build.Preflighter); ok {
		err = preflighter.Preflight(ctx, h.Context)
		if err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
	}

	before, err := stampResults(h.Context.Dirs)
	if err != nil {
		return err
	}

	err = bld.Run(ctx, h.Context)
	if err != nil {
		return err
	}

	after, err := stampResults(h.Context.Dirs)
	if err != nil {
		return err
	}

	return checkProduced(h.backend, bld, h.Context.Dirs, before, after)
}

// fileStamp tells a file that a build wrote from one it left alone.
type fileStamp struct {
	size    int64
	modTime time.Time
}

// stamps are the files of the directories of the results of a build, by
// type and by their paths relative to the directory of the type.
type stamps map[build.Result]map[string]fileStamp

// stampResults stamps the files of the directories of dirs of the types of
// results; a directory a hook has not is left out.
func stampResults(dirs build.Dirs) (stamps, error) {
	stamped := stamps{}

	for _, result := range build.Results() {
		dir := dirs.Of(result)
		if dir == "" {
			continue
		}

		files := map[string]fileStamp{}

		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}

			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("stamping %s: %w", path, err)
			}

			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return fmt.Errorf("stamping %s: %w", path, err)
			}

			files[rel] = fileStamp{size: info.Size(), modTime: info.ModTime()}

			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("buildtest: %w", err)
		}

		stamped[result] = files
	}

	return stamped, nil
}

// checkProduced checks that what bld, a build of backend, made in dirs
// between the stamps before and after is of the types backend produces,
// and so are the programs it names.
func checkProduced(backend build.Backend, bld build.Build, dirs build.Dirs,
	before, after stamps,
) error {
	for _, result := range build.Results() {
		if slices.Contains(backend.Produces, result) {
			continue
		}

		var made []string

		for path, stamp := range after[result] {
			if old, ok := before[result][path]; !ok || old != stamp {
				made = append(made, filepath.Join(dirs.Of(result), path))
			}
		}

		if namer, ok := bld.(build.ProgramNamer); ok && result == build.Program {
			names, _ := namer.Programs()

			made = append(made, names...)
		}

		if len(made) > 0 {
			slices.Sort(made)

			return fmt.Errorf("buildtest: %w: backend %q makes no %s: %s", build.ErrNotProduced,
				backend.Name, result, strings.Join(slices.Compact(made), ", "))
		}
	}

	return nil
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

	program, lookErr := lookPath(name, environ)
	if lookErr != nil {
		program = name
	}

	cmd := exec.CommandContext(ctx, program, args...)
	if lookErr != nil {
		// Running it fails as running a program that is not there does,
		// even when the PATH of the test has it.
		cmd.Path = name
		cmd.Err = lookErr
	}

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
	if !strings.Contains(name, "/") {
		return lookPath(name, s.environ())
	}

	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.h.Context.Dirs.Cwd, path)
	}

	path, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("buildtest: looking for %s: %w", name, err)
	}

	return path, nil
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

	return append(environ, "PATH="+facts.Dirs.Tools+string(os.PathListSeparator)+path)
}

// lookPath finds name in the last PATH of environ, and only there, as the
// core does: a name with a slash is taken as it is, a relative entry of
// PATH is skipped, and a name found nowhere is an *exec.Error wrapping
// exec.ErrNotFound.
func lookPath(name string, environ []string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}

	path, _ := build.LookupEnv(environ, "PATH")

	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}

		candidate := filepath.Join(dir, name)

		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}

	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
