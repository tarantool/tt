package manifest

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// nameRe is the identifier shape shared by package, component and product
// names: a lowercase letter followed by lowercase letters, digits and dashes.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func isReservedPackageName(name string) bool {
	switch name {
	case "bin", "manifests":
		return true
	default:
		return false
	}
}

func isPlatformToken(token string) bool {
	switch token {
	case "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "any":
		return true
	default:
		return false
	}
}

func isHookBackend(backend string) bool {
	switch backend {
	case backendMake, backendShell:
		return true
	default:
		return false
	}
}

func isHookName(name string) bool {
	switch name {
	case "pre_build", "post_build":
		return true
	default:
		return false
	}
}

// Validate walks the manifest structure and reports the first structural error
// it finds, always as a *ValidationError. The returned warnings are non-fatal:
// unknown hook names are skipped with a warning, since hooks are an extension
// point for later phases.
//
// Validation here is structural - fields present and consistent with each
// other. Whether a dependency actually resolves, the package version, and on-disk
// component files are checked further down the pipeline.
func (m *Manifest) Validate() ([]string, error) {
	checks := []func() error{
		m.validateVersion,
		m.validatePackage,
		m.validatePlatform,
		func() error { return validateDependencies("dependencies", m.Dependencies) },
		func() error { return validateDependencies("dev_dependencies", m.DevDependencies) },
		m.validateComponents,
		m.validateProducts,
	}

	for _, check := range checks {
		err := check()
		if err != nil {
			return nil, err
		}
	}

	return m.validateHooks()
}

func (m *Manifest) validateVersion() error {
	if m.ManifestVersion == "" {
		return invalid("manifest_version", "is required")
	}

	ver, err := parseFormatVersion(m.ManifestVersion)
	if err != nil {
		return err
	}

	if ver.major != ourManifestVersion.major {
		return fmt.Errorf("manifest version %q %w (supports %d.x)",
			ver, ErrUnsupportedVersion, ourManifestVersion.major)
	}

	return nil
}

// ValidPackageName reports whether name is a well-formed, non-reserved package
// name. Commands that take a package name as an argument and turn it into a
// path — uninstall, which removes manifests/<name>/ and the rock subtrees named
// after it — must check it first: the shape is what rules out a traversal or a
// name that would resolve onto the metadata directory itself.
func ValidPackageName(name string) bool {
	return nameRe.MatchString(name) && !isReservedPackageName(name)
}

func (m *Manifest) validatePackage() error {
	name := m.Package.Name
	switch {
	case name == "":
		return invalid("package.name", "is required")
	case !nameRe.MatchString(name):
		return invalid("package.name", "%q must match [a-z][a-z0-9-]*", name)
	case isReservedPackageName(name):
		return invalid("package.name", "%q is reserved", name)
	}

	return nil
}

func (m *Manifest) validatePlatform() error {
	switch {
	case m.Platform.Tarantool.Version == "":
		return invalid("platform.tarantool", "is required")
	case m.Platform.Tt.Version == "":
		return invalid("platform.tt", "is required")
	case m.Platform.Tcm.Flavor != "":
		return invalid("platform.tcm", "must not have a flavor; TCM is Enterprise only")
	}

	err := m.validatePlatforms()
	if err != nil {
		return err
	}

	return m.validateRegistries()
}

// validateRegistries rejects a registry list that cannot be queried: an empty
// table, a blank entry, or the same server twice. A duplicate is refused rather
// than deduplicated because the order is the resolution order, and a list that
// names one server twice says the author meant two different ones.
func (m *Manifest) validateRegistries() error {
	registries := m.Platform.Registries
	if registries == nil {
		return nil
	}

	if len(registries) == 0 {
		return invalid("platform.registries", "must be omitted or non-empty")
	}

	seen := make(map[string]bool, len(registries))

	for _, registry := range registries {
		if strings.TrimSpace(registry) == "" {
			return invalid("platform.registries", "must not contain an empty entry")
		}

		if seen[registry] {
			return invalid("platform.registries", "%q is listed twice", registry)
		}

		seen[registry] = true
	}

	return nil
}

func (m *Manifest) validatePlatforms() error {
	platforms := m.Platform.Platforms
	if platforms == nil {
		return nil
	}

	if len(platforms) == 0 {
		return invalid("platform.platforms", "must be omitted or non-empty")
	}

	hasAny := false
	hasConcrete := false

	for _, p := range platforms {
		if !isPlatformToken(p) {
			return invalid("platform.platforms", "unknown token %q", p)
		}

		if p == "any" {
			hasAny = true
		} else {
			hasConcrete = true
		}
	}

	if hasAny && hasConcrete {
		return invalid("platform.platforms", "%q cannot be mixed with concrete platforms", "any")
	}

	return nil
}

func validateDependencies(section string, deps map[string]Dependency) error {
	for _, name := range sortedKeys(deps) {
		err := validateDependency(section+"."+name, deps[name])
		if err != nil {
			return err
		}
	}

	return nil
}

func validateDependency(field string, dep Dependency) error {
	switch dep.Source {
	case sourceRegistry:
		if dep.Version == "" {
			return invalid(field, "version is required for source %q", sourceRegistry)
		}
	case sourcePath:
		if dep.Path == "" {
			return invalid(field, "path is required for source %q", sourcePath)
		}
	default:
		return invalid(field,
			"source %q is not supported in manifest_version %q (want registry or path)",
			dep.Source, ManifestVersion)
	}

	if dep.Kind != "" && dep.Kind != "library" {
		return invalid(field,
			"kind %q is not supported in manifest_version %q (want library)",
			dep.Kind, ManifestVersion)
	}

	return nil
}

func (m *Manifest) validateComponents() error {
	// programs maps a program an executable component builds to that
	// component: two of them building one name would overwrite each other's
	// file in .rocks/bin/.
	programs := map[string]string{}

	for _, name := range sortedKeys(m.Components) {
		comp := m.Components[name]

		field := "components." + name
		if !nameRe.MatchString(name) {
			return invalid(field, "name must match [a-z][a-z0-9-]*")
		}

		if comp.Path == "" {
			return invalid(field+".path", "is required")
		}

		err := validateDependencies(field+".dependencies", comp.Dependencies)
		if err != nil {
			return err
		}

		err = validateKind(field, comp)
		if err != nil {
			return err
		}

		if comp.Build != nil {
			err := validateBuild(field+".build", *comp.Build)
			if err != nil {
				return err
			}

			err = validateKindBackend(field+".build", comp)
			if err != nil {
				return err
			}
		}

		if !comp.IsExecutable() {
			continue
		}

		for _, program := range comp.Build.Executables(name) {
			if other, taken := programs[program]; taken {
				return invalid(field+".build",
					"program %q is built by component %q too", program, other)
			}

			programs[program] = name
		}
	}

	return nil
}

// validateKind checks the kind of a component and what the kind rules out.
// An executable component is made by its build alone: it lays no files out,
// so include, exclude and namespace have nothing to act on, and without a
// build it would make nothing.
func validateKind(field string, comp Component) error {
	switch comp.Kind {
	case "", KindModule:
		return nil
	case KindExecutable:
	default:
		return invalid(field+".kind",
			"%q is not supported in manifest_version %q (want %s or %s)",
			comp.Kind, ManifestVersion, KindModule, KindExecutable)
	}

	notForPrograms := []struct {
		name string
		set  bool
	}{
		{"include", comp.Include != nil},
		{"exclude", comp.Exclude != nil},
		{"namespace", comp.Namespace != nil},
	}

	for _, key := range notForPrograms {
		if key.set {
			return invalid(field+"."+key.name,
				"is not allowed for kind %q: its build places its programs in .rocks/bin",
				KindExecutable)
		}
	}

	if comp.Build == nil {
		return invalid(field+".build", "is required for kind %q", KindExecutable)
	}

	return nil
}

// validateKindBackend checks that the backend of a component's build makes
// what the kind of the component is: go and cargo make programs only, c and
// lua-c make modules only, make and shell make either.
func validateKindBackend(field string, comp Component) error {
	backend := comp.Build.Backend

	switch {
	case comp.IsExecutable() && (backend == backendC || backend == backendLuaC):
		return invalid(field, "backend %q builds a module, not a program (kind %q wants "+
			"go, cargo, make or shell)", backend, KindExecutable)
	case !comp.IsExecutable() && isProgramBackend(backend):
		return invalid(field, "backend %q builds a program: set kind = %q on the component",
			backend, KindExecutable)
	}

	return nil
}

// isProgramBackend reports whether backend builds programs only.
func isProgramBackend(backend string) bool {
	return backend == backendGo || backend == backendCargo
}

// programNameRe is the shape of a program name a go or cargo build is given:
// a plain file name, which tt instance finds in .rocks/bin/ by the binary of
// an instance description given without a slash.
var programNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]*$`)

// cargoProfileRe is the shape cargo allows a profile name.
var cargoProfileRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateBuild(field string, build Build) error {
	switch build.Backend {
	case backendMake:
		if build.MakeTarget == "" {
			return invalid(field, "make_target is required for backend %q", backendMake)
		}
	case backendShell:
		if build.Command == "" {
			return invalid(field, "command is required for backend %q", backendShell)
		}
	case backendC, backendLuaC:
		if build.Module == "" {
			return invalid(field, "module is required for backend %q", build.Backend)
		}

		if len(build.Sources) == 0 {
			return invalid(field, "sources is required for backend %q", build.Backend)
		}
	case backendGo:
		err := validateGoBuild(field, build)
		if err != nil {
			return err
		}
	case backendCargo:
		err := validateCargoBuild(field, build)
		if err != nil {
			return err
		}
	case "":
		return invalid(field, "backend is required")
	default:
		return invalid(field,
			"backend %q is not supported in manifest_version %q "+
				"(want make, shell, c, lua-c, go or cargo)",
			build.Backend, ManifestVersion)
	}

	return validateBackendFields(field, build)
}

func validateGoBuild(field string, build Build) error {
	if build.Name != "" && !programNameRe.MatchString(build.Name) {
		return invalid(field+".name", "%q is not a file name", build.Name)
	}

	for _, tag := range build.Tags {
		if tag == "" || strings.ContainsAny(tag, ", \t\n") {
			return invalid(field+".tags", "%q is not a build tag", tag)
		}
	}

	if slices.Contains(build.Ldflags, "") {
		return invalid(field+".ldflags", "must not contain an empty entry")
	}

	return nil
}

func validateCargoBuild(field string, build Build) error {
	if build.Bin != "" && !programNameRe.MatchString(build.Bin) {
		return invalid(field+".bin", "%q is not a file name", build.Bin)
	}

	for _, feature := range build.Features {
		if feature == "" || strings.ContainsAny(feature, ", \t\n") {
			return invalid(field+".features", "%q is not a feature name", feature)
		}
	}

	if build.Profile != "" && !cargoProfileRe.MatchString(build.Profile) {
		return invalid(field+".profile", "%q is not a profile name", build.Profile)
	}

	return nil
}

// backendField is a field of a build block one or more backends own.
type backendField struct {
	name   string
	set    bool
	owners []string
}

// backendFields lists the fields of build that belong to some backends only,
// with whether each is set.
func backendFields(build Build) []backendField {
	ccDriver := []string{backendC, backendLuaC}

	return []backendField{
		{"make_target", build.MakeTarget != "", []string{backendMake}},
		{"entrypoint", build.Entrypoint != "", []string{backendMake}},
		{"flags", build.Flags != nil, []string{backendMake}},
		{"command", build.Command != "", []string{backendShell}},
		{"args", build.Args != nil, []string{backendShell}},
		{"module", build.Module != "", ccDriver},
		{"sources", build.Sources != nil, ccDriver},
		{"include_dirs", build.IncludeDirs != nil, ccDriver},
		{"libraries", build.Libraries != nil, ccDriver},
		{"library_dirs", build.LibraryDirs != nil, ccDriver},
		{"defines", build.Defines != nil, ccDriver},
		{"platforms", build.Platforms != nil, ccDriver},
		// go and cargo name their program themselves.
		{"output", build.Output != nil, []string{backendMake, backendShell, backendC, backendLuaC}},
		{"package", build.Package != "", []string{backendGo, backendCargo}},
		{"static", build.Static != nil, []string{backendGo, backendCargo}},
		{"name", build.Name != "", []string{backendGo}},
		{"tags", build.Tags != nil, []string{backendGo}},
		{"ldflags", build.Ldflags != nil, []string{backendGo}},
		{"cgo", build.Cgo != nil, []string{backendGo}},
		{"bin", build.Bin != "", []string{backendCargo}},
		{"features", build.Features != nil, []string{backendCargo}},
		{"profile", build.Profile != "", []string{backendCargo}},
	}
}

// validateBackendFields rejects a field of another backend: a go build with
// make_target, a make build with static. Such a field does nothing, and a
// user who wrote static = true on a make build expects a static program he
// would not get.
//
// The fields the go and cargo backends brought, and every field on a go or
// cargo build, are checked. A field of make, shell, c or lua-c on a build of
// another of those four is still let through, as it always was: a manifest
// that carries one would stop building.
func validateBackendFields(field string, build Build) error {
	for _, key := range backendFields(build) {
		if !key.set || slices.Contains(key.owners, build.Backend) {
			continue
		}

		legacyField := !slices.ContainsFunc(key.owners, isProgramBackend)
		if legacyField && !isProgramBackend(build.Backend) {
			continue
		}

		return invalid(field+"."+key.name, "is a field of backend %s, not of %q",
			strings.Join(key.owners, " or "), build.Backend)
	}

	return nil
}

func (m *Manifest) validateProducts() error {
	names := sortedKeys(m.Products)
	defaults := 0

	for _, name := range names {
		field := "products." + name
		if !nameRe.MatchString(name) {
			// The "::" guard gets a dedicated message since it is reserved
			// rather than merely malformed.
			if regexp.MustCompile(`::`).MatchString(name) {
				return invalid(field,
					"must not contain %q (reserved for cross-package composition)", "::")
			}

			return invalid(field, "name must match [a-z][a-z0-9-]*")
		}

		p := m.Products[name]
		if p.Default {
			defaults++
		}

		for _, comp := range p.Components {
			if _, ok := m.Components[comp]; !ok {
				return invalid(field, "references unknown component %q", comp)
			}
		}
	}

	if len(names) > 1 && defaults != 1 {
		return invalid("products",
			"exactly one product must have default = true (found %d)", defaults)
	}

	return nil
}

func (m *Manifest) validateHooks() ([]string, error) {
	var warnings []string

	for _, name := range sortedKeys(m.Hooks) {
		if !isHookName(name) {
			warnings = append(warnings, fmt.Sprintf(
				"unknown hook %q skipped (only pre_build and post_build are supported)", name))

			continue
		}

		hook := m.Hooks[name]

		backend := hook.Backend
		if backend == "" {
			backend = backendShell // Hooks default to shell.
		}

		if !isHookBackend(backend) {
			return warnings, invalid("hooks."+name,
				"backend %q is not supported (want make or shell)", backend)
		}

		hook.Backend = backend

		err := validateBuild("hooks."+name, hook)
		if err != nil {
			return warnings, err
		}
	}

	return warnings, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
