package buildreg

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// ErrNoBackend reports a build block of a backend the registry does not
// have.
var ErrNoBackend = errors.New("no module of this tt provides backend")

// CheckBuild checks block, the build block at field, which builds target,
// for its backend, as manifest.Validate has it checked (manifest.BuildChecker).
// The checks go in this order, and the first error is reported:
//
//  1. a hook names a backend that runs hooks (build.Backend.Hook);
//  2. the fields of block.Rest are decoded into the configuration of the
//     backend (build.Backend.Decode): a value of a wrong type fails it;
//  3. the fields the configuration has no place for and no other backend
//     has are the unknown ones, which Validate reports;
//  4. the backend checks the block (build.Backend.New);
//  5. a field of another backend is refused, by key, then output and
//     incremental, for a backend that has no such field;
//  6. the kind of the component is one the backend builds; a hook has none.
//
// A block of a component whose backend the registry does not have is not
// checked. The programs of an executable component are those its build
// names (build.ProgramNamer).
func (r *Registry) CheckBuild(
	field string, target manifest.BuildTarget, block manifest.Build,
) (manifest.BuildCheck, error) {
	found, ok := r.lookup(block.Backend)

	check := manifest.BuildCheck{Known: ok, Unknown: nil, Programs: nil}

	if target.Hook && (!ok || !found.backend.Hook) {
		return check, &manifest.ValidationError{
			Field: field,
			Msg: fmt.Sprintf("backend %q is not supported (want %s)", block.Backend,
				orList(r.hookNames())),
		}
	}

	if !ok {
		return check, nil
	}

	cfg, keys, err := found.backend.Decode(block.Rest)
	if err != nil {
		return check, validationError(field, err)
	}

	var foreign []string

	for _, key := range keys {
		// A key of a table of the configuration that the table has no
		// place for is no field of another backend.
		if !strings.ContainsAny(key, ".[") && len(r.owners(key)) > 0 {
			foreign = append(foreign, key)
		} else {
			check.Unknown = append(check.Unknown, key)
		}
	}

	bld, err := found.backend.New(cfg, component(target), common(block))
	if err != nil {
		return check, validationError(field, err)
	}

	err = r.checkForeign(field, found, block, foreign)
	if err == nil && !target.Hook {
		err = r.checkKind(field, found, target)
	}

	if err != nil {
		return check, err
	}

	if namer, ok := bld.(build.ProgramNamer); ok && target.Kind == build.KindExecutable {
		check.Programs, _ = namer.Programs()
	}

	return check, nil
}

// Build returns the backend of block, the build block at field which builds
// target, and the build its New makes of the block: what the build of
// target runs. The block is to have been checked (CheckBuild): the fields
// of other backends and those no backend has are not looked at again. A
// block of a backend the registry does not have is ErrNoBackend.
func (r *Registry) Build(
	field string, target manifest.BuildTarget, block manifest.Build,
) (build.Backend, build.Build, error) {
	found, ok := r.lookup(block.Backend)
	if !ok {
		return build.Backend{}, nil, fmt.Errorf("%w %q", ErrNoBackend, block.Backend)
	}

	cfg, _, err := found.backend.Decode(block.Rest)
	if err != nil {
		return build.Backend{}, nil, validationError(field, err)
	}

	bld, err := found.backend.New(cfg, component(target), common(block))
	if err != nil {
		return build.Backend{}, nil, validationError(field, err)
	}

	return found.backend, bld, nil
}

// component is the component target, as a backend is told it: the zero one
// for a hook.
func component(target manifest.BuildTarget) build.Component {
	return build.Component{
		Name:      target.Component,
		Kind:      target.Kind,
		Path:      target.Path,
		Namespace: target.Namespace,
		Package:   target.Package,
	}
}

// common is what a backend is told of the fields every build block has.
func common(block manifest.Build) build.Common {
	return build.Common{
		Cwd: block.Cwd, Env: block.Env, Output: block.Output, Static: block.StaticValue(),
	}
}

// owners returns the names of the backends that have the field key of a
// build block, in the order of Names.
func (r *Registry) owners(key string) []string {
	var names []string

	for _, e := range r.ordered() {
		if e.has(key) {
			names = append(names, e.backend.Name)
		}
	}

	return names
}

// hookNames returns the names of the backends that run hooks, in the order
// of Names.
func (r *Registry) hookNames() []string {
	var names []string

	for _, e := range r.ordered() {
		if e.backend.Hook {
			names = append(names, e.backend.Name)
		}
	}

	return names
}

// has reports whether the backend of e has the field key: one of its
// configuration, or output and incremental when it allows them.
func (e entry) has(key string) bool {
	switch key {
	case "output":
		return e.backend.OutputFrom != build.OutputNone
	case "incremental":
		return e.backend.Incremental
	default:
		return slices.Contains(e.fields, key)
	}
}

// checkForeign refuses a field of block the backend of found does not have:
// those of foreign, the fields of other backends, in key order, then output
// and incremental. A field is the block's when its key is there, whatever
// its value: make_target = "" on a go block is refused as make_target =
// "all" is.
func (r *Registry) checkForeign(
	field string, found entry, block manifest.Build, foreign []string,
) error {
	if block.Output != nil {
		foreign = append(foreign, "output")
	}

	if block.Incremental != nil {
		foreign = append(foreign, "incremental")
	}

	for _, key := range foreign {
		if found.has(key) {
			continue
		}

		return &manifest.ValidationError{
			Field: field + "." + key,
			Msg: fmt.Sprintf("is a field of backend %s, not of %q",
				orList(r.owners(key)), found.backend.Name),
		}
	}

	return nil
}

// checkKind checks that the backend of found builds a component of the kind
// of target.
func (r *Registry) checkKind(field string, found entry, target manifest.BuildTarget) error {
	if slices.Contains(found.backend.Kinds, target.Kind) {
		return nil
	}

	if target.Kind == build.KindExecutable {
		var builders []string

		for _, e := range r.ordered() {
			if slices.Contains(e.backend.Kinds, build.KindExecutable) {
				builders = append(builders, e.backend.Name)
			}
		}

		return &manifest.ValidationError{
			Field: field,
			Msg: fmt.Sprintf("backend %q builds a module, not a program (kind %q wants %s)",
				found.backend.Name, build.KindExecutable, orList(builders)),
		}
	}

	return &manifest.ValidationError{
		Field: field,
		Msg: fmt.Sprintf("backend %q builds a program: set kind = %q on the component",
			found.backend.Name, build.KindExecutable),
	}
}

// validationError is err, of a check of the build block at field, as
// manifest.Validate reports it: a *manifest.ValidationError, with the path
// of the field a build.FieldError names after field.
func validationError(field string, err error) error {
	if validation, ok := errors.AsType[*manifest.ValidationError](err); ok {
		return validation
	}

	if fieldErr, ok := errors.AsType[*build.FieldError](err); ok {
		path := field
		if fieldErr.Field != "" {
			path += "." + fieldErr.Field
		}

		return &manifest.ValidationError{Field: path, Msg: fieldErr.Msg}
	}

	return &manifest.ValidationError{Field: field, Msg: err.Error()}
}

// orList joins names as a sentence lists them: "a", "a or b", "a, b or c".
func orList(names []string) string {
	last := len(names) - 1
	if last <= 0 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:last], ", ") + " or " + names[last]
}
