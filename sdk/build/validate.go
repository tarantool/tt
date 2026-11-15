package build

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// This file checks that what a module provides is well formed: the core
// checks every Build a backend makes, and buildtest checks the same with the
// same functions, so that a backend its tests pass is one the core takes.

// ErrBackend reports a Build of a backend that is not well formed
// ([Backend.ValidateBuild]).
var ErrBackend = errors.New("invalid build backend")

// optionalInterfaces are the optional interfaces of a Build, each by the
// name of its method.
var optionalInterfaces = []struct {
	method string
	iface  reflect.Type
}{
	{"Preflight", reflect.TypeFor[Preflighter]()},
	{"Produces", reflect.TypeFor[Producer]()},
	{"Programs", reflect.TypeFor[ProgramNamer]()},
	{"Inputs", reflect.TypeFor[InputHasher]()},
}

// ValidateBuild checks bld, a Build New of b returned: that a method of it
// named as the method of an optional interface - Preflight, Produces,
// Programs, Inputs - makes it that interface ([Preflighter], [Producer],
// [ProgramNamer], [InputHasher]). The core finds these interfaces by type
// assertion, so a method of another signature, written for an older SDK,
// or one of a pointer receiver on a Build New returns as a value, would
// never be called: the check it makes, or the inputs it names, would be
// left out with no error. The core checks every Build a backend makes,
// when it checks a manifest, and so does buildtest. The error wraps
// [ErrBackend] and says what signature the method needs.
func (b Backend) ValidateBuild(bld Build) error {
	if bld == nil {
		return fmt.Errorf("%w %q: New returned a nil Build", ErrBackend, b.Name)
	}

	typ := reflect.TypeOf(bld)

	for _, optional := range optionalInterfaces {
		if typ.Implements(optional.iface) {
			continue
		}

		want, _ := optional.iface.MethodByName(optional.method)

		if method, ok := typ.MethodByName(optional.method); ok {
			return fmt.Errorf("%w %q: %s of its build %s is %s, not %s of build.%s: "+
				"the core would never call it", ErrBackend, b.Name, optional.method, typ,
				methodSignature(method.Type), want.Type, optional.iface.Name())
		}

		if typ.Kind() == reflect.Pointer {
			continue
		}

		if _, ok := reflect.PointerTo(typ).MethodByName(optional.method); ok {
			return fmt.Errorf("%w %q: %s of its build is a method of *%s, but New returns "+
				"a %s: the core would never call it", ErrBackend, b.Name, optional.method, typ,
				typ)
		}
	}

	return nil
}

// methodSignature is the type of a method of a type, as reflect has it,
// without its receiver: func(context.Context, *build.Context) error.
func methodSignature(method reflect.Type) reflect.Type {
	params := slices.Collect(method.Ins())
	results := slices.Collect(method.Outs())

	return reflect.FuncOf(params[1:], results, method.IsVariadic())
}
