package buildreg

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// blockSetter is a configuration that takes the fields every build block
// has before the backend checks it: the Spec the backends of the core share
// (corebackend.Spec.SetBlock), whose checks read them with the fields of the
// backend, in the order they were checked in before the backends had fields
// of their own. A backend of a module gets them as build.Common instead.
type blockSetter interface {
	SetBlock(build manifest.Build)
}

// CheckBuild checks block, the build block at field, which builds target,
// for its backend, as manifest.Validate has it checked (manifest.BuildChecker).
// The checks go in this order, and the first error is reported:
//
//  1. the fields of block.Rest are decoded into the configuration of the
//     backend (build.Backend.Decode): a value of a wrong type fails it;
//  2. the fields the configuration has no place for and no other backend
//     has are the unknown ones, which Validate reports;
//  3. the backend checks the block (build.Backend.New);
//  4. a field of another backend is refused, by key, then output and
//     incremental, for a backend that has no such field;
//  5. the kind of the component is one the backend builds; a hook has none.
//
// A block of a backend the registry does not have is not checked.
func (r *Registry) CheckBuild(
	field string, target manifest.BuildTarget, block manifest.Build,
) (manifest.BuildCheck, error) {
	found, ok := r.lookup(block.Backend)
	if !ok {
		return manifest.BuildCheck{Known: false, Unknown: nil}, nil
	}

	check := manifest.BuildCheck{Known: true, Unknown: nil}

	cfg, keys, err := found.backend.Decode(block.Rest)
	if err != nil {
		return check, validationError(field, err)
	}

	if setter, ok := cfg.(blockSetter); ok {
		setter.SetBlock(block)
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

	_, err = found.backend.New(cfg, component(target), common(block))
	if err != nil {
		return check, validationError(field, err)
	}

	err = r.checkForeign(field, found, block, foreign)
	if err == nil && !target.Hook {
		err = r.checkKind(field, found, target)
	}

	return check, err
}

// component is the component target, as a backend is told it.
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

// has reports whether the backend of e has the field key: one of its
// configuration, or output and incremental when it allows them.
func (e entry) has(key string) bool {
	switch {
	case e.owns != nil:
		return e.owns(key)
	case key == "output":
		return e.backend.OutputFrom != build.OutputNone
	case key == "incremental":
		return e.backend.Incremental
	default:
		return slices.Contains(e.fields, key)
	}
}

// checkForeign refuses a field of block the backend of found does not have:
// those of foreign, the fields of other backends, in key order, then output
// and incremental.
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
// of target. It is the one check of the kind of a component the backends of
// modules get.
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
