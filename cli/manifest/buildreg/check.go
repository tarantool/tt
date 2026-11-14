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
//  6. what the block asks for is what the backend makes
//     (build.Backend.Produces): the to of each file of output, then the
//     types the build says it makes (build.Producer); then the deprecated
//     kind of the component, when it has one, says what the backend can
//     make and what the build makes. A hook makes nothing that is checked.
//
// A block of a component whose backend the registry does not have is not
// checked. The programs of a component are those its build names
// (build.ProgramNamer).
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

	bld, err := found.backend.New(cfg, component(target), common(block, target))
	if err != nil {
		return check, validationError(field, err)
	}

	err = r.checkForeign(field, found, block, foreign)
	if err == nil && !target.Hook {
		err = checkResults(field, found.backend, bld, block, target)
	}

	if err != nil {
		return check, err
	}

	if namer, ok := bld.(build.ProgramNamer); ok && !target.Hook {
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

	bld, err := found.backend.New(cfg, component(target), common(block, target))
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
		Path:      target.Path,
		Namespace: target.Namespace,
		Package:   target.Package,
		Expects:   Expects(target.Kind),
	}
}

// Expects returns the types of what a component of the deprecated kind
// says its build makes (build.Component.Expects): nil for none.
func Expects(kind string) []build.Result {
	switch kind {
	case manifest.KindExecutable:
		return []build.Result{build.Program}
	case manifest.KindModule:
		return []build.Result{build.Module, build.Data}
	default:
		return nil
	}
}

// common is what a backend is told of the fields every build block has,
// the block building target.
func common(block manifest.Build, target manifest.BuildTarget) build.Common {
	var output []build.OutputFile

	if block.Output != nil {
		output = make([]build.OutputFile, 0, len(block.Output))
	}

	for _, entry := range block.Output {
		output = append(output, build.OutputFile{
			Path: entry.Path, To: OutputType(entry, target.Kind),
		})
	}

	return build.Common{
		Cwd: block.Cwd, Env: block.Env, Output: output, Static: block.StaticValue(),
	}
}

// OutputType returns the type of the file of output entry of a component of
// the deprecated kind: where the entry says it goes, or, for an entry that
// says nothing, a module - a program for kind = "executable".
func OutputType(entry manifest.OutputEntry, kind string) build.Result {
	switch entry.To {
	case manifest.OutputToShare:
		return build.Data
	case manifest.OutputToBin:
		return build.Program
	case manifest.OutputToLib:
		return build.Module
	}

	if kind == manifest.KindExecutable {
		return build.Program
	}

	return build.Module
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

// checkResults checks that what block, the build block at field of the
// component of target, asks for is what backend makes, and that the build
// bld of the block makes what the deprecated kind of the component says it
// does: kind = "executable" a backend that makes programs, and a build that
// makes nothing else; kind = "module" a backend that makes modules or data,
// and a build that makes no program. What a build makes besides what it
// says it does is checked after it ran.
func checkResults(
	field string, backend build.Backend, bld build.Build, block manifest.Build,
	target manifest.BuildTarget,
) error {
	for index, entry := range block.Output {
		result := OutputType(entry, target.Kind)
		if slices.Contains(backend.Produces, result) {
			continue
		}

		entryField := fmt.Sprintf("%s.output[%d]", field, index)
		if entry.To != "" {
			entryField += ".to"
		}

		return &manifest.ValidationError{
			Field: entryField,
			Msg:   fmt.Sprintf("backend %q makes no %s", backend.Name, Plural(result)),
		}
	}

	var asked []build.Result

	if producer, ok := bld.(build.Producer); ok {
		asked = producer.Produces()
	}

	for _, result := range asked {
		if !slices.Contains(backend.Produces, result) {
			return &manifest.ValidationError{
				Field: field,
				Msg:   fmt.Sprintf("backend %q makes no %s", backend.Name, Plural(result)),
			}
		}
	}

	expects := Expects(target.Kind)
	if expects == nil {
		return nil
	}

	kindField := strings.TrimSuffix(field, ".build") + ".kind"

	if !slices.ContainsFunc(expects, func(result build.Result) bool {
		return slices.Contains(backend.Produces, result)
	}) {
		what := "no " + Plural(build.Program)
		if target.Kind == manifest.KindModule {
			what = Plural(build.Program) + " only"
		}

		return &manifest.ValidationError{
			Field: kindField,
			Msg:   fmt.Sprintf("is %q, but backend %q makes %s", target.Kind, backend.Name, what),
		}
	}

	for _, result := range asked {
		if !slices.Contains(expects, result) {
			return &manifest.ValidationError{
				Field: kindField,
				Msg:   fmt.Sprintf("is %q, but the build makes %s", target.Kind, Plural(result)),
			}
		}
	}

	return nil
}

// Plural names the results of the type result in a message: modules, data
// files, programs.
func Plural(result build.Result) string {
	if result == build.Data {
		return "data files"
	}

	return string(result) + "s"
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
