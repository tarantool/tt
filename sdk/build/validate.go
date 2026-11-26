package build

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// This file checks that what a module provides is well formed: the core
// checks a backend and a file check before it adds them, and every Build a
// backend makes; buildtest checks the same with the same functions, so that
// a backend its tests pass is one the core takes.

var (
	// ErrBackend reports a backend that is not well formed
	// ([Backend.Validate]), or a Build of it that is not
	// ([Backend.ValidateBuild]).
	ErrBackend = errors.New("invalid build backend")
	// ErrFileCheck reports a file check that is not well formed
	// ([FileCheck.Validate]).
	ErrFileCheck = errors.New("invalid file check")
)

// CommonFields returns the keys of the fields every build block has,
// whatever its backend - those of [Common], incremental and backend:
// backend, cwd, env, output, incremental and static. They are the core's,
// and the configuration of no backend has one ([Backend.Config]).
func CommonFields() []string {
	return []string{"backend", "cwd", "env", "output", "incremental", "static"}
}

// nameRe is the shape of the name of a backend: the shape of the names of a
// manifest.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

var (
	// errProduces reports the result types of a backend that are not well
	// formed.
	errProduces = errors.New("the Produces must name module, data, program or link, once each")
	// errExclude reports an empty pattern of the exclusions of a backend.
	errExclude = errors.New("an empty pattern in Exclude")
	// errOutputFrom reports an OutputFrom of no meaning.
	errOutputFrom = errors.New("OutputFrom is none of OutputNone, OutputCwd and OutputBackend")
)

// Validate checks that b is well formed, as the core checks a backend a
// module provides before it adds it, and as buildtest checks the backend
// of a test: its Name has the shape of a name, it has New, its Config
// returns what the decoder can fill and has none of [CommonFields], its
// Produces name one of [Results] at least and each at most once, its
// OutputFrom is one of the three, and no pattern of its Exclude is empty.
// That no other backend of the tt has its name is the core's to check. The
// error wraps [ErrBackend] and names the backend; one of the configuration
// wraps [ErrConfig] too.
func (b Backend) Validate() error {
	if !nameRe.MatchString(b.Name) {
		return fmt.Errorf("%w %q: the name must match [a-z][a-z0-9-]*", ErrBackend, b.Name)
	}

	if b.New == nil {
		return fmt.Errorf("%w %q: no New", ErrBackend, b.Name)
	}

	err := checkProduces(b.Produces)
	if err == nil {
		err = checkOutputFrom(b.OutputFrom)
	}

	if err == nil && slices.Contains(b.Exclude, "") {
		err = errExclude
	}

	if err != nil {
		return fmt.Errorf("%w %q: %w", ErrBackend, b.Name, err)
	}

	fields, err := b.Fields()
	if err != nil {
		return fmt.Errorf("%w %q: %w", ErrBackend, b.Name, err)
	}

	for _, field := range fields {
		if slices.Contains(CommonFields(), field) {
			return fmt.Errorf("%w %q: %s is a field of every build block, not one of a backend",
				ErrBackend, b.Name, field)
		}
	}

	return nil
}

// checkProduces checks the types of what a backend makes.
func checkProduces(produces []Result) error {
	if len(produces) == 0 {
		return errProduces
	}

	for i, result := range produces {
		if !slices.Contains(Results(), result) || slices.Contains(produces[:i], result) {
			names := make([]string, 0, len(produces))
			for _, result := range produces {
				names = append(names, string(result))
			}

			return fmt.Errorf("%w, not %q", errProduces, strings.Join(names, ", "))
		}
	}

	return nil
}

// checkOutputFrom checks where the output of a backend comes from.
func checkOutputFrom(from OutputFrom) error {
	switch from {
	case OutputNone, OutputCwd, OutputBackend:
		return nil
	default:
		return fmt.Errorf("%w: %d", errOutputFrom, from)
	}
}

// Validate checks that c is well formed, as the core checks a file check a
// module provides before it adds it: it has a Suffix and a Check. The error
// wraps [ErrFileCheck].
func (c FileCheck) Validate() error {
	switch {
	case c.Suffix == "":
		return fmt.Errorf("%w: no Suffix", ErrFileCheck)
	case c.Check == nil:
		return fmt.Errorf("%w of %q: no Check", ErrFileCheck, c.Suffix)
	}

	return nil
}

// exportMethod is the method of [Exporter].
const exportMethod = "Export"

// optionalInterfaces are the optional interfaces of a Build, each by the
// name of its method.
var optionalInterfaces = []struct {
	method string
	iface  reflect.Type
}{
	{"Preflight", reflect.TypeFor[Preflighter]()},
	{"Produces", reflect.TypeFor[Producer]()},
	{"Programs", reflect.TypeFor[ProgramNamer]()},
	{"Links", reflect.TypeFor[Linker]()},
	{"Inputs", reflect.TypeFor[InputHasher]()},
	{"Clean", reflect.TypeFor[Cleaner]()},
	{"Vendor", reflect.TypeFor[Vendorer]()},
	{exportMethod, reflect.TypeFor[Exporter]()},
}

// ValidateBuild checks bld, a Build New of b returned: that a method of it
// named as the method of an optional interface - Preflight, Produces,
// Programs, Links, Inputs, Clean, Vendor - makes it that interface
// ([Preflighter], [Producer], [ProgramNamer], [Linker], [InputHasher],
// [Cleaner], [Vendorer]), and that it is an [Exporter] when b says its
// builds are made without tt ([Backend.Exports]), and only then. The core
// finds these interfaces by type assertion, so a method of another
// signature, written for an older SDK, or one of a pointer receiver on a
// Build New returns as a value, would never be called: the check it makes,
// the inputs it names, or what it cleans would be left out with no error.
// A method Export of a backend that does not say its builds are made without
// tt is checked only when it is that of Exporter, which the backend is then
// to say: one of another signature is a method of the backend's own, which
// the core never looks for. The core checks every Build a backend makes,
// when it checks a manifest, and so does buildtest. The error wraps
// [ErrBackend] and says what signature the method needs, or what the
// backend is to say.
func (b Backend) ValidateBuild(bld Build) error {
	if bld == nil {
		return fmt.Errorf("%w %q: New returned a nil Build", ErrBackend, b.Name)
	}

	typ := reflect.TypeOf(bld)

	for _, optional := range optionalInterfaces {
		if typ.Implements(optional.iface) || (optional.method == exportMethod && !b.Exports) {
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

	return b.checkExports(typ)
}

// checkExports reports a build of b, of the type typ, that is an [Exporter]
// when b does not say its builds are made without tt ([Backend.Exports]),
// or is none when it says so.
func (b Backend) checkExports(typ reflect.Type) error {
	exports := typ.Implements(reflect.TypeFor[Exporter]())

	switch {
	case b.Exports && !exports:
		return fmt.Errorf("%w %q: the backend says its builds are made without tt "+
			"(Exports), and its build %s has no Export of build.Exporter", ErrBackend, b.Name,
			typ)
	case !b.Exports && exports:
		return fmt.Errorf("%w %q: its build %s has Export of build.Exporter, and the "+
			"backend does not say its builds are made without tt: set Exports", ErrBackend,
			b.Name, typ)
	default:
		return nil
	}
}

// methodSignature is the type of a method of a type, as reflect has it,
// without its receiver: func(context.Context, *build.Context) error.
func methodSignature(method reflect.Type) reflect.Type {
	params := slices.Collect(method.Ins())
	results := slices.Collect(method.Outs())

	return reflect.FuncOf(params[1:], results, method.IsVariadic())
}
