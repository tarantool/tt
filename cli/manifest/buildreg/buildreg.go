// Package buildreg is the registry of the build backends a tt has and of
// the checks of the files its builds lay out: those the modules tt is built
// from contribute (sdk.Module). The core has no backend of its own; the
// backends of tt are those of the standard module build.
//
// The core fills the registry of the process once, before any command runs
// (SetProcess); what checks a manifest or builds a component reads it
// (Process). A Registry checks the build blocks of a manifest for their
// backends (manifest.BuildChecker).
package buildreg

import (
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/tarantool/tt/sdk/build"
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
	// module is the name of the module that provides the backend.
	module string
	// fields are the keys of its configuration (build.Backend.Fields).
	fields []string
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{entries: nil, checks: nil}
}

var (
	// ErrBackend reports a backend a module cannot provide: one that is not
	// well formed (build.Backend.Validate), or named as another.
	ErrBackend = build.ErrBackend
	// ErrFileCheck reports a file check a module cannot provide
	// (build.FileCheck.Validate).
	ErrFileCheck = build.ErrFileCheck
)

// Add registers the backends and the file checks the module named module
// provides. A backend is refused, and so is a file check, that is not well
// formed - build.Backend.Validate and build.FileCheck.Validate, which
// buildtest checks a backend with too - and a backend named as another one
// of the registry is; the rest are added. The error names the module and
// joins the error of each one refused.
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
		err := check.Validate()
		if err != nil {
			errs = append(errs, fmt.Errorf("module %q: %w", module, err))

			continue
		}

		r.checks = append(r.checks, check)
	}

	return errors.Join(errs...)
}

// Backend returns the backend name and the name of the module that provides
// it.
func (r *Registry) Backend(name string) (build.Backend, string, bool) {
	e, ok := r.lookup(name)

	return e.backend, e.module, ok
}

// Names returns the names of the backends in the order they were added: the
// core adds the backends of the modules in the order of the names of the
// modules, those of each in the order the module gives them. It is the order
// the messages that list backends list them in.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.entries))

	for _, e := range r.entries {
		names = append(names, e.backend.Name)
	}

	return names
}

// FileChecks returns the file checks, in the order they were added: those
// of the modules in the order of their names.
func (r *Registry) FileChecks() []build.FileCheck {
	return slices.Clone(r.checks)
}

// check checks backend, of module, before it is added: that it is well
// formed (build.Backend.Validate) and that no backend of the registry has
// its name.
func (r *Registry) check(module string, backend build.Backend) (entry, error) {
	err := backend.Validate()
	if err != nil {
		// Validate names the backend; Add names the module.
		return entry{}, err
	}

	if other, ok := r.lookup(backend.Name); ok {
		return entry{}, fmt.Errorf("%w %q: module %q provides one of the name too",
			ErrBackend, backend.Name, other.module)
	}

	fields, err := backend.Fields()
	if err != nil {
		return entry{}, fmt.Errorf("%w %q: %w", ErrBackend, backend.Name, err)
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

// process is the registry of the process.
var process atomic.Pointer[Registry]

// Process returns the registry of the process: the one SetProcess set, or,
// in a process that set none, such as a test, an empty one.
func Process() *Registry {
	if registry := process.Load(); registry != nil {
		return registry
	}

	return New()
}

// SetProcess makes registry the registry of the process. The core calls it
// once it has called the constructors of the modules, before any command
// runs, and nothing changes the registry after.
func SetProcess(registry *Registry) {
	process.Store(registry)
}
