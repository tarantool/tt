package backend

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	sdkbuild "github.com/tarantool/tt/sdk/build"
)

// This file is the check of the build block of a backend of the core, which
// manifest.Validate runs through the registry of backends (Core): what each
// backend requires and rules out of its fields, the fields of one backend on
// the block of another, and what the kind of the component asks of the
// backend. The errors are those of manifest.Validate, the fields named from
// the block (sdkbuild.FieldError).

var (
	// errNotSpec reports a configuration of a backend of the core that is
	// not the Spec the backend decodes into.
	errNotSpec = errors.New("the configuration of a backend of the core is not a Spec")
	// errNotRun reports a build of a backend of the core run through the
	// SDK, which runs them through New instead.
	errNotRun = errors.New("a build of a backend of the core runs through backend.New")
)

// Core returns the backends of the core: make and shell, then the others by
// name, the order messages list them in. They share one configuration, Spec,
// whose fields do not tell which backend has which: Owns does.
func Core() []sdkbuild.Backend {
	both := []string{sdkbuild.KindModule, sdkbuild.KindExecutable}
	module := []string{sdkbuild.KindModule}
	program := []string{sdkbuild.KindExecutable}

	return []sdkbuild.Backend{
		coreBackend(BackendMake, both, true, sdkbuild.OutputCwd, nil),
		coreBackend(BackendShell, both, true, sdkbuild.OutputCwd, nil),
		coreBackend(BackendC, module, false, sdkbuild.OutputNone, nil),
		coreBackend(BackendCargo, both, false, sdkbuild.OutputNone, []string{cargoTargetDir}),
		coreBackend(BackendCMake, both, false, sdkbuild.OutputBackend, nil),
		coreBackend(BackendGo, program, false, sdkbuild.OutputNone, nil),
		coreBackend(BackendLuaC, module, false, sdkbuild.OutputNone, nil),
	}
}

// cargoTargetDir is the target directory cargo makes in the component when
// it is run by hand, which the layout of the component leaves out.
const cargoTargetDir = "/target/"

// coreBackend is the backend name of the core.
func coreBackend(
	name string, kinds []string, incremental bool, output sdkbuild.OutputFrom, exclude []string,
) sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:        name,
		Kinds:       kinds,
		Config:      newSpec,
		New:         newCoreBuild,
		Incremental: incremental,
		OutputFrom:  output,
		Exclude:     exclude,
	}
}

// newCoreBuild checks spec, the build block of component decoded into a Spec
// whose block fields were set (Spec.SetBlock), in the order the block was
// checked in before the backends had fields of their own: what its backend
// requires and rules out, the fields of other backends, then what the kind
// of the component asks of the backend.
func newCoreBuild(
	cfg any, component sdkbuild.Component, _ sdkbuild.Common,
) (sdkbuild.Build, error) {
	spec, ok := cfg.(*Spec)
	if !ok {
		return nil, fmt.Errorf("%w: %T", errNotSpec, cfg)
	}

	err := checkSpec(*spec)
	if err != nil {
		return nil, err
	}

	err = checkKind(*spec, component.Kind == sdkbuild.KindExecutable)
	if err != nil {
		return nil, err
	}

	return coreBuild{spec: *spec}, nil
}

// coreBuild is a checked build block of a backend of the core.
type coreBuild struct {
	spec Spec
}

// Run refuses to run: the build of the core runs the executors of this
// package (New) with the block decoded again (Decode).
func (b coreBuild) Run(context.Context, *sdkbuild.Context) error {
	return fmt.Errorf("%w: %s", errNotRun, b.spec.Backend)
}

// checkSpec checks what the backend of spec requires and rules out of its
// fields, then the fields of other backends on it.
func checkSpec(spec Spec) error {
	var err error

	switch spec.Backend {
	case BackendMake:
		if spec.MakeTarget == "" {
			return sdkbuild.FieldErrorf("", "make_target is required for backend %q", BackendMake)
		}
	case BackendShell:
		if spec.Command == "" {
			return sdkbuild.FieldErrorf("", "command is required for backend %q", BackendShell)
		}
	case BackendC, BackendLuaC:
		err = checkCc(spec)
	case BackendGo:
		err = checkGo(spec)
	case BackendCargo:
		err = checkCargo(spec)
	case BackendCMake:
		err = checkCMake(spec)
	}

	if err != nil {
		return err
	}

	return checkBackendFields(spec)
}

// checkKind checks that the backend of spec makes what the kind of the
// component is, executable or not: go makes programs only, c and lua-c make
// modules only, cargo, cmake, make and shell make either.
func checkKind(spec Spec, executable bool) error {
	switch {
	case executable && (spec.Backend == BackendC || spec.Backend == BackendLuaC):
		return sdkbuild.FieldErrorf("", "backend %q builds a module, not a program "+
			"(kind %q wants go, cargo, cmake, make or shell)",
			spec.Backend, sdkbuild.KindExecutable)
	case !executable && spec.Backend == BackendGo:
		return sdkbuild.FieldErrorf("", "backend %q builds a program: set kind = %q on the "+
			"component", spec.Backend, sdkbuild.KindExecutable)
	case spec.Backend == BackendCargo:
		return checkCargoKind(spec, executable)
	}

	return nil
}

// checkCargoKind checks the fields of a cargo build that belong to one kind:
// bin to the binary of an executable component, module to the library of a
// module component.
func checkCargoKind(spec Spec, executable bool) error {
	switch {
	case executable && spec.Module != "":
		return sdkbuild.FieldErrorf("module", "names the library of a module component; "+
			"kind %q builds the binary bin", sdkbuild.KindExecutable)
	case executable:
		return nil
	case spec.Bin != "":
		return sdkbuild.FieldErrorf("bin", "names the binary of an executable component; "+
			"a module component builds the library of the package, named by module")
	}

	return nil
}

// programNameRe is the shape of a program name a go or cargo build is given:
// a plain file name, which tt instance finds in .rocks/bin/ by the binary of
// an instance description given without a slash.
var programNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]*$`)

// cargoProfileRe is the shape cargo allows a profile name.
var cargoProfileRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// cargoModuleRe is the shape of the module a cargo build makes: the file is
// <module>.so in the namespace of the component, and the module is a name of
// require and of the C functions of box.schema.func.create, so no dot.
var cargoModuleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// cmakeVariableRe is the shape of a key of variables of a cmake build: the
// name of a cache variable, with its type after a colon if it is given one,
// as -D<name>[:<type>]=<value> takes it. An = would end the name early; a
// space and a leading dash are kept out of it as out of a target.
var cmakeVariableRe = regexp.MustCompile(`^[A-Za-z0-9_.+][A-Za-z0-9_.+-]*(:[A-Z]+)?$`)

// cmakeNameRe is the shape of a target and of a build type of a cmake build:
// no space, and no leading dash, which cmake --build would take for an
// option.
var cmakeNameRe = regexp.MustCompile(`^[A-Za-z0-9_.+][A-Za-z0-9_.+-]*$`)

// cmakeBuildTypeVariable is the variable build_type of a cmake build sets.
const cmakeBuildTypeVariable = "CMAKE_BUILD_TYPE"

// checkCc checks the fields of a c or lua-c build: module and sources are
// required, env sets none of the variables that reach nothing there
// (ccEnvVars), each overlay of platforms is for an OS tt builds for, and no
// flag of the compiler or of the linker, of the build or of an overlay, is
// empty, which cc would take for the name of a file.
func checkCc(spec Spec) error {
	if spec.Module == "" {
		return sdkbuild.FieldErrorf("", "module is required for backend %q", spec.Backend)
	}

	if len(spec.Sources) == 0 {
		return sdkbuild.FieldErrorf("", "sources is required for backend %q", spec.Backend)
	}

	for _, variable := range ccEnvVars {
		if _, set := spec.Env[variable.name]; set {
			return sdkbuild.FieldErrorf("env."+variable.name, "does nothing on backend %q: %s",
				spec.Backend, variable.instead)
		}
	}

	flags := []flagList{{"cflags", spec.Cflags}, {"ldflags", spec.Ldflags}}
	oses := sdkbuild.SupportedOS()

	for _, goos := range sortedKeys(spec.Platforms) {
		// A part for an OS tt builds for none of the platforms of would never
		// be added: platforms.macos for darwin, say.
		if !slices.Contains(oses, goos) {
			return sdkbuild.FieldErrorf("platforms."+goos,
				"%q is not an OS tt builds for (want %s)", goos, orList(oses))
		}

		overlay := spec.Platforms[goos]
		prefix := "platforms." + goos + "."

		flags = append(flags,
			flagList{prefix + "cflags", overlay.Cflags},
			flagList{prefix + "ldflags", overlay.Ldflags})
	}

	for _, key := range flags {
		if slices.Contains(key.flags, "") {
			return sdkbuild.FieldErrorf(key.name, "must not contain an empty entry")
		}
	}

	return nil
}

// flagList is a list of flags of a build, with the name of its field.
type flagList struct {
	name  string
	flags []string
}

// ccEnvVars are the variables of make and autotools a user would set in env
// of a c or lua-c build to compile it with, with what to write instead. The
// compiler reads none of them, so one of env reaches nothing: CC, CFLAGS and
// LDFLAGS tt reads from the environment it runs in to put the cc line
// together, CPPFLAGS, LIBS and LDLIBS from nowhere, since the build has
// fields of its own for what they carry.
var ccEnvVars = []struct {
	name    string
	instead string
}{
	{"CC", "the compiler is CC of the environment tt runs in, cc by default"},
	{"CFLAGS", "put the flags of the compiler into cflags; " +
		"CFLAGS of the environment tt runs in comes after them"},
	{"CPPFLAGS", "put the macros into defines, the directories of headers into " +
		"include_dirs and other flags of the preprocessor into cflags"},
	{"LDFLAGS", "put the flags of the link into ldflags; " +
		"LDFLAGS of the environment tt runs in comes after them"},
	{"LIBS", "put the libraries into libraries and their directories into library_dirs"},
	{"LDLIBS", "put the libraries into libraries and their directories into library_dirs"},
}

// checkCMake checks the fields of a cmake build. output is required: the
// build tree of cmake holds the objects, the caches and the generated files
// of the build as well, so the build takes from it the files output names
// and nothing else.
func checkCMake(spec Spec) error {
	if len(spec.Output) == 0 {
		return sdkbuild.FieldErrorf("", "output is required for backend %q: the files of its "+
			"build tree the build takes", BackendCMake)
	}

	for _, output := range spec.Output {
		switch {
		case output == "":
			return sdkbuild.FieldErrorf("output", "must not contain an empty entry")
		case filepath.IsAbs(output):
			return sdkbuild.FieldErrorf("output", "%q is not relative to the build tree", output)
		}
	}

	for _, name := range sortedKeys(spec.Variables) {
		if !cmakeVariableRe.MatchString(name) {
			return sdkbuild.FieldErrorf("variables", "%q is not a variable name: letters, "+
				"digits and _.+-, not - first, then :<TYPE> if it has one", name)
		}

		if variable, _, _ := strings.Cut(name, ":"); variable == cmakeBuildTypeVariable {
			return sdkbuild.FieldErrorf("variables", "%s is set by build_type",
				cmakeBuildTypeVariable)
		}
	}

	for _, target := range spec.Targets {
		if !cmakeNameRe.MatchString(target) {
			return sdkbuild.FieldErrorf("targets", "%q is not a target name", target)
		}
	}

	if spec.BuildType != "" && !cmakeNameRe.MatchString(spec.BuildType) {
		return sdkbuild.FieldErrorf("build_type", "%q is not a build type", spec.BuildType)
	}

	return nil
}

// checkGo checks the fields of a go build.
func checkGo(spec Spec) error {
	if spec.Name != "" && !programNameRe.MatchString(spec.Name) {
		return sdkbuild.FieldErrorf("name", "%q is not a file name", spec.Name)
	}

	for _, tag := range spec.Tags {
		if tag == "" || strings.ContainsAny(tag, ", \t\n") {
			return sdkbuild.FieldErrorf("tags", "%q is not a build tag", tag)
		}
	}

	if slices.Contains(spec.Ldflags, "") {
		return sdkbuild.FieldErrorf("ldflags", "must not contain an empty entry")
	}

	return nil
}

// checkCargo checks the fields of a cargo build.
func checkCargo(spec Spec) error {
	if spec.Bin != "" && !programNameRe.MatchString(spec.Bin) {
		return sdkbuild.FieldErrorf("bin", "%q is not a file name", spec.Bin)
	}

	if spec.Module != "" && !cargoModuleRe.MatchString(spec.Module) {
		return sdkbuild.FieldErrorf("module", "%q is not a module name: a letter or _, "+
			"then letters, digits and _", spec.Module)
	}

	for _, feature := range spec.Features {
		if feature == "" || strings.ContainsAny(feature, ", \t\n") {
			return sdkbuild.FieldErrorf("features", "%q is not a feature name", feature)
		}
	}

	if spec.Profile != "" && !cargoProfileRe.MatchString(spec.Profile) {
		return sdkbuild.FieldErrorf("profile", "%q is not a profile name", spec.Profile)
	}

	return nil
}

// backendField is a field of a build block one or more backends own.
type backendField struct {
	name   string
	set    bool
	owners []string
}

// backendFields lists the fields of spec that belong to some backends only,
// with whether each is set. static is not one: the build of any component
// checks what it made (manifest.Build.Static), and a hook, which makes
// nothing it checks, is refused one by manifest.Validate.
func backendFields(spec Spec) []backendField {
	ccDriver := []string{BackendC, BackendLuaC}
	linkers := []string{BackendGo, BackendC, BackendLuaC}

	return []backendField{
		{"make_target", spec.MakeTarget != "", []string{BackendMake}},
		{"entrypoint", spec.Entrypoint != "", []string{BackendMake}},
		{"flags", spec.Flags != nil, []string{BackendMake}},
		{"command", spec.Command != "", []string{BackendShell}},
		{"args", spec.Args != nil, []string{BackendShell}},
		{"module", spec.Module != "", []string{BackendC, BackendLuaC, BackendCargo}},
		{"sources", spec.Sources != nil, ccDriver},
		{"include_dirs", spec.IncludeDirs != nil, ccDriver},
		{"libraries", spec.Libraries != nil, ccDriver},
		{"library_dirs", spec.LibraryDirs != nil, ccDriver},
		{"defines", spec.Defines != nil, ccDriver},
		{"cflags", spec.Cflags != nil, ccDriver},
		{"platforms", spec.Platforms != nil, ccDriver},
		// Flags of the linker of each: go build's -ldflags, the link of cc.
		{"ldflags", spec.Ldflags != nil, linkers},
		// The backends that copy the files it lists: go and cargo name their
		// program themselves, c and lua-c write their module.
		{"output", spec.Output != nil, []string{BackendMake, BackendShell, BackendCMake}},
		{"incremental", spec.Incremental != nil, []string{BackendMake, BackendShell}},
		{"package", spec.Package != "", []string{BackendGo, BackendCargo}},
		{"name", spec.Name != "", []string{BackendGo}},
		{"tags", spec.Tags != nil, []string{BackendGo}},
		{"cgo", spec.Cgo != nil, []string{BackendGo}},
		{"bin", spec.Bin != "", []string{BackendCargo}},
		{"features", spec.Features != nil, []string{BackendCargo}},
		{"profile", spec.Profile != "", []string{BackendCargo}},
		{"variables", spec.Variables != nil, []string{BackendCMake}},
		{"targets", spec.Targets != nil, []string{BackendCMake}},
		{"build_type", spec.BuildType != "", []string{BackendCMake}},
	}
}

// checkBackendFields rejects a field of another backend, on the build of
// every backend and of a hook: a go build with make_target, a make build
// with cgo or cflags, a shell build with make_target. Such a field does
// nothing, and a user who wrote cflags on a make build expects a build with
// those flags he would not get.
func checkBackendFields(spec Spec) error {
	for _, key := range backendFields(spec) {
		if !key.set || slices.Contains(key.owners, spec.Backend) {
			continue
		}

		return sdkbuild.FieldErrorf(key.name, "is a field of backend %s, not of %q",
			orList(key.owners), spec.Backend)
	}

	return nil
}

// Owns reports whether the backend of the core name has the field key of a
// build block, output and incremental included. The backends of the core
// share one configuration, Spec, so its fields do not tell.
func Owns(name, key string) bool {
	for _, field := range backendFields(Spec{}) { //nolint:exhaustruct // No field is set.
		if field.name == key {
			return slices.Contains(field.owners, name)
		}
	}

	return false
}

// orList joins names as a sentence lists them: "a", "a or b", "a, b or c".
func orList(names []string) string {
	last := len(names) - 1
	if last <= 0 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:last], ", ") + " or " + names[last]
}

// sortedKeys returns the keys of m in byte order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
