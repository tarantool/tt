// Package buildreg is the registry of the build backends a tt has and of
// the checks of the files its builds lay out: those of the core, and those
// the modules tt is built from contribute (sdk.Module).
//
// The core fills the registry of the process once, before any command runs
// (SetProcess); what checks a manifest or builds a component reads it
// (Process). A Registry checks the build blocks of a manifest for their
// backends (manifest.BuildChecker).
package buildreg

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	corebackend "github.com/tarantool/tt/v3/cli/manifest/build/backend"
)

// Registry holds the build backends and the file checks of a tt. It is
// filled before it is read, and is not safe for concurrent use while it is
// filled.
type Registry struct {
	// entries are the backends, in the order they were added.
	entries []entry
	// checks are the file checks, in the order they were added.
	checks []build.FileCheck
}

// entry is a backend of a registry.
type entry struct {
	backend build.Backend
	// module is the name of the module that provides the backend; empty
	// for one of the core.
	module string
	// fields are the keys of its configuration (build.Backend.Fields).
	fields []string
	// owns reports whether the backend has the field key, when its fields
	// do not tell: the backends of the core share one configuration
	// (corebackend.Owns). Nil for the others.
	owns func(key string) bool
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{entries: nil, checks: nil}
}

// Core returns a registry of the backends of the core (corebackend.Core),
// in their order: make and shell, then the others by name.
func Core() *Registry {
	registry := New()

	for _, provided := range corebackend.Core() {
		added, err := registry.check("", provided)
		if err != nil {
			// The backends of the core are this package's to keep well
			// formed: TestCore checks them.
			panic(fmt.Sprintf("buildreg: %v", err))
		}

		name := provided.Name

		added.owns = func(key string) bool { return corebackend.Owns(name, key) }

		registry.entries = append(registry.entries, added)
	}

	return registry
}

var (
	// ErrBackend reports a backend a module cannot provide.
	ErrBackend = errors.New("invalid build backend")
	// ErrFileCheck reports a file check a module cannot provide.
	ErrFileCheck = errors.New("invalid file check")
)

// nameRe is the shape of the name of a backend: the shape of the names of a
// manifest.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Add registers the backends and the file checks the module named module
// provides. A backend is refused, and so is a file check, that is not well
// formed - see the errors - and a backend named as another one of the
// registry is; the rest are added. The error names the module and joins the
// error of each one refused.
func (r *Registry) Add(module string, backends []build.Backend, checks []build.FileCheck) error {
	var errs []error

	for _, provided := range backends {
		added, err := r.check(module, provided)
		if err != nil {
			errs = append(errs, fmt.Errorf("module %q: %w", module, err))

			continue
		}

		r.entries = append(r.entries, added)
	}

	for _, check := range checks {
		err := checkFileCheck(check)
		if err != nil {
			errs = append(errs, fmt.Errorf("module %q: %w", module, err))

			continue
		}

		r.checks = append(r.checks, check)
	}

	return errors.Join(errs...)
}

var (
	// errKinds reports the kinds of a backend that are not well formed.
	errKinds = errors.New("the Kinds must name module, executable or both, once each")
	// errExclude reports an empty pattern of the exclusions of a backend.
	errExclude = errors.New("an empty pattern in Exclude")
)

// checkKinds checks the kinds a backend builds.
func checkKinds(kinds []string) error {
	if len(kinds) == 0 {
		return errKinds
	}

	for i, kind := range kinds {
		if kind != build.KindModule && kind != build.KindExecutable ||
			slices.Contains(kinds[:i], kind) {
			return fmt.Errorf("%w, not %q", errKinds, strings.Join(kinds, ", "))
		}
	}

	return nil
}

// errOutputFrom reports an OutputFrom of no meaning.
var errOutputFrom = errors.New("OutputFrom is none of OutputNone, OutputCwd and OutputBackend")

// checkOutputFrom checks where the output of a backend comes from.
func checkOutputFrom(from build.OutputFrom) error {
	switch from {
	case build.OutputNone, build.OutputCwd, build.OutputBackend:
		return nil
	default:
		return fmt.Errorf("%w: %d", errOutputFrom, from)
	}
}

// checkFileCheck checks a file check before it is added.
func checkFileCheck(check build.FileCheck) error {
	switch {
	case check.Suffix == "":
		return fmt.Errorf("%w: no Suffix", ErrFileCheck)
	case check.Check == nil:
		return fmt.Errorf("%w of %q: no Check", ErrFileCheck, check.Suffix)
	}

	return nil
}

// provider names who provides the backend of e, for an error.
func (e entry) provider() string {
	if e.module == "" {
		return "the core"
	}

	return fmt.Sprintf("module %q", e.module)
}

// Backend returns the backend name and the name of the module that provides
// it, empty for one of the core.
func (r *Registry) Backend(name string) (build.Backend, string, bool) {
	e, ok := r.lookup(name)

	return e.backend, e.module, ok
}

// Names returns the names of the backends: those of the core in the order
// the core added them, then those of the modules by name. It is the order
// the messages that list backends list them in.
func (r *Registry) Names() []string {
	var ofCore, ofModules []string

	for _, e := range r.entries {
		if e.module == "" {
			ofCore = append(ofCore, e.backend.Name)
		} else {
			ofModules = append(ofModules, e.backend.Name)
		}
	}

	slices.Sort(ofModules)

	return append(ofCore, ofModules...)
}

// FileChecks returns the file checks, in the order they were added: those
// of the modules in the order of their names.
func (r *Registry) FileChecks() []build.FileCheck {
	return slices.Clone(r.checks)
}

// check checks backend, of module, before it is added.
func (r *Registry) check(module string, backend build.Backend) (entry, error) {
	if !nameRe.MatchString(backend.Name) {
		return entry{}, fmt.Errorf("%w %q: the name must match [a-z][a-z0-9-]*",
			ErrBackend, backend.Name)
	}

	if other, ok := r.lookup(backend.Name); ok {
		return entry{}, fmt.Errorf("%w %q: %s provides one of the name too",
			ErrBackend, backend.Name, other.provider())
	}

	if backend.New == nil {
		return entry{}, fmt.Errorf("%w %q: no New", ErrBackend, backend.Name)
	}

	err := checkKinds(backend.Kinds)
	if err == nil {
		err = checkOutputFrom(backend.OutputFrom)
	}

	if err == nil && slices.Contains(backend.Exclude, "") {
		err = errExclude
	}

	if err != nil {
		return entry{}, fmt.Errorf("%w %q: %w", ErrBackend, backend.Name, err)
	}

	fields, err := backend.Fields()
	if err != nil {
		return entry{}, fmt.Errorf("%w %q: %w", ErrBackend, backend.Name, err)
	}

	for _, field := range fields {
		if slices.Contains(manifest.CommonBuildFields(), field) {
			return entry{}, fmt.Errorf("%w %q: %s is a field of every build block, "+
				"not one of a backend", ErrBackend, backend.Name, field)
		}
	}

	return entry{backend: backend, module: module, fields: fields}, nil
}

// lookup returns the entry of the backend name.
func (r *Registry) lookup(name string) (entry, bool) {
	for _, e := range r.entries {
		if e.backend.Name == name {
			return e, true
		}
	}

	return entry{}, false
}

// ordered returns the entries in the order of Names.
func (r *Registry) ordered() []entry {
	names := r.Names()
	entries := make([]entry, 0, len(names))

	for _, name := range names {
		found, _ := r.lookup(name)

		entries = append(entries, found)
	}

	return entries
}

var (
	// process is the registry of the process.
	process atomic.Pointer[Registry]
	// core is the registry of the backends of the core, which a process
	// whose registry nobody set has.
	core = sync.OnceValue(Core)
)

// Process returns the registry of the process: the one SetProcess set, or,
// in a process that set none, such as a test, that of the backends of the
// core.
func Process() *Registry {
	if registry := process.Load(); registry != nil {
		return registry
	}

	return core()
}

// SetProcess makes registry the registry of the process. The core calls it
// once it has called the constructors of the modules, before any command
// runs, and nothing changes the registry after.
func SetProcess(registry *Registry) {
	process.Store(registry)
}
