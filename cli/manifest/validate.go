package manifest

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest/native"
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

// isPlatformToken reports whether token names a platform of
// [platform].platforms: "any", or one of those tt builds for
// (native.Supported).
func isPlatformToken(token string) bool {
	return token == "any" || slices.ContainsFunc(native.Supported(), func(p native.Platform) bool {
		return p.String() == token
	})
}

func isHookName(name string) bool {
	switch name {
	case "pre_build", "post_build", "clean":
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
//
// The fields of the backend of a build block are checked by checker, in the
// place of the block among the checks; a nil checker checks the structure
// only, the fields of no backend. A block of a backend checker does not know
// is a warning: the build refuses it, but a command that builds nothing can
// work with the manifest still.
func (m *Manifest) Validate(checker BuildChecker) ([]string, error) {
	var (
		warnings []string
		links    map[string][]string
	)

	checks := []func() error{
		m.validateVersion,
		m.validatePackage,
		m.validatePlatform,
		func() error { return validateDependencies("dependencies", m.Dependencies) },
		func() error { return validateDependencies("dev_dependencies", m.DevDependencies) },
		func() error {
			componentWarnings, componentLinks, err := m.validateComponents(checker)

			warnings = append(warnings, componentWarnings...)
			links = componentLinks

			return err
		},
		m.validateProducts,
		func() error { return m.validateLinks(links) },
	}

	for _, check := range checks {
		err := check()
		if err != nil {
			return nil, err
		}
	}

	hookWarnings, err := m.validateHooks(checker)

	return append(warnings, hookWarnings...), err
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

// validateComponents checks the components, and returns the warnings of
// their checks and the components whose libraries the build of each links,
// by component (BuildCheck.Links).
func (m *Manifest) validateComponents(
	checker BuildChecker,
) ([]string, map[string][]string, error) {
	// programs maps a program a component builds to that component: two of
	// them building one name would overwrite each other's file in
	// .rocks/bin/.
	programs := map[string]string{}
	links := map[string][]string{}

	var warnings []string

	for _, name := range sortedKeys(m.Components) {
		comp := m.Components[name]

		field := "components." + name
		if !nameRe.MatchString(name) {
			return nil, nil, invalid(field, "name must match [a-z][a-z0-9-]*")
		}

		if comp.Path == "" {
			return nil, nil, invalid(field+".path", "is required")
		}

		err := validateDependencies(field+".dependencies", comp.Dependencies)
		if err != nil {
			return nil, nil, err
		}

		err = validateKind(field, comp)
		if err != nil {
			return nil, nil, err
		}

		if comp.Kind != "" {
			warnings = append(warnings, fmt.Sprintf("%s.kind is deprecated: a component "+
				"makes what its build makes; remove it", field))
		}

		if comp.Build == nil {
			continue
		}

		check, buildWarnings, err := m.validateComponentBuild(checker, name, comp)

		warnings = append(warnings, buildWarnings...)

		if err != nil {
			return nil, nil, err
		}

		for _, program := range check.Programs {
			if other, taken := programs[program]; taken {
				return nil, nil, invalid(field+".build",
					"program %q is built by component %q too", program, other)
			}

			programs[program] = name
		}

		if len(check.Links) > 0 {
			links[name] = check.Links
		}
	}

	return warnings, links, nil
}

// validateLinks checks the components whose libraries the build of each
// component of links links: each is a component of the package, and every
// product of the component has it and builds it first, in the order of its
// components, so that what it made for linking is there when the build that
// links it runs. The product of a manifest without [products] builds its
// components in the order of their names.
func (m *Manifest) validateLinks(links map[string][]string) error {
	for _, name := range sortedKeys(links) {
		for index, other := range links[name] {
			if _, ok := m.Components[other]; !ok {
				return invalid(fmt.Sprintf("components.%s.build.link[%d]", name, index),
					"%q is no component of the package", other)
			}
		}
	}

	products := m.EffectiveProducts()

	for _, product := range sortedKeys(products) {
		components := products[product].Components

		for at, name := range components {
			for index, other := range links[name] {
				before := slices.Index(components, other)
				if before >= 0 && before < at {
					continue
				}

				if len(m.Products) == 0 {
					return invalid(fmt.Sprintf("components.%s.build.link[%d]", name, index),
						"component %q is built after %q, as the components of a manifest "+
							"without [products] are built in the order of their names: declare "+
							"a product that lists %q before %q", other, name, other, name)
				}

				what := "has no component " + strconv.Quote(other)
				if before >= 0 {
					what = "builds it after " + strconv.Quote(name)
				}

				return invalid("products."+product+".components",
					"component %q links the libraries of component %q, and the product %s: "+
						"list %q before %q", name, other, what, other, name)
			}
		}
	}

	return nil
}

// validateComponentBuild checks the build block of the component name: it
// names a backend, and checker, when there is one, checks the block for it.
// A backend checker does not know is a warning.
func (m *Manifest) validateComponentBuild(
	checker BuildChecker, name string, comp Component,
) (BuildCheck, []string, error) {
	field := "components." + name + ".build"
	if comp.Build.Backend == "" {
		return BuildCheck{}, nil, invalid(field, "backend is required")
	}

	err := validateOutput(field+".output", comp.Build.Output)
	if err != nil {
		return BuildCheck{}, nil, err
	}

	if checker == nil {
		return BuildCheck{}, nil, nil
	}

	check, warnings, err := m.checkBuild(checker, field, m.ComponentTarget(name), *comp.Build)
	if err != nil || check.Known {
		return check, warnings, err
	}

	return check, []string{fmt.Sprintf("%s: no module of this tt provides backend %q; "+
		"its fields are not checked", field, comp.Build.Backend)}, nil
}

// checkBuild has checker check build, the build block at field of target,
// and returns what it found. The fields checker finds no backend has are
// reported by the format-version rules of the fields of the rest of the
// manifest: an error for a manifest of this minor version or an older one,
// before any error of the backend, and a warning for one of a newer minor
// version.
func (m *Manifest) checkBuild(
	checker BuildChecker, field string, target BuildTarget, build Build,
) (BuildCheck, []string, error) {
	check, err := checker.CheckBuild(field, target, build)
	if len(check.Unknown) == 0 {
		return check, nil, err
	}

	declared, versionErr := parseFormatVersion(m.ManifestVersion)
	if versionErr != nil {
		return check, nil, versionErr
	}

	if !declared.minorNewerThan(ourManifestVersion) {
		return check, nil, invalid(field+"."+check.Unknown[0],
			"unknown field in manifest version %q", declared)
	}

	warnings := make([]string, 0, len(check.Unknown))
	for _, key := range check.Unknown {
		warnings = append(warnings, fmt.Sprintf(
			"field %q appeared in a newer manifest version; trying to ignore it", field+"."+key))
	}

	return check, warnings, err
}

// validateOutput checks the entries of output, at field: each has a path,
// and goes to lib, share, bin or link.
func validateOutput(field string, output []OutputEntry) error {
	for i, entry := range output {
		entryField := fmt.Sprintf("%s[%d]", field, i)

		switch entry.To {
		case "", OutputToLib, OutputToShare, OutputToBin, OutputToLink:
		default:
			return invalid(entryField+".to", "%q is not %s, %s, %s or %s", entry.To,
				OutputToLib, OutputToShare, OutputToBin, OutputToLink)
		}

		if entry.Path == "" && entry.To != "" {
			return invalid(entryField+".path", "is required")
		}
	}

	return nil
}

// validateKind checks the deprecated kind of a component and what the kind
// rules out. A component of kind = "executable" is made by its build alone:
// it lays no files out, so include, exclude and namespace have nothing to
// act on, and without a build it would make nothing. What its build makes
// is checked against the kind by the checker and by the build.
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

	return m.validateProductRocks()
}

// validateProductRocks checks the rocks the products are released as: a
// name of a rock, one rock per product, and no component in two of them,
// since the rocks would own the same files in a tree that installs both.
func (m *Manifest) validateProductRocks() error {
	owners := map[string]string{} // Rock name -> product.
	parts := map[string]string{}  // Component -> rock.

	for _, name := range sortedKeys(m.Products) {
		field := "products." + name + ".rock"

		if rock := m.Products[name].Rock; rock != "" && !nameRe.MatchString(rock) {
			return invalid(field, "name must match [a-z][a-z0-9-]*")
		}

		rock := m.ProductRock(name)
		if rock == "" {
			continue
		}

		if other, ok := owners[rock]; ok {
			return invalid(field, "the product %s is released as the rock %q too", other, rock)
		}

		owners[rock] = name

		for _, component := range m.Products[name].Components {
			if other, ok := parts[component]; ok {
				return invalid(field, "the component %q is in the rock %q too: a tree "+
					"installing both rocks would have its files twice", component, other)
			}

			parts[component] = rock
		}
	}

	return nil
}

// validateHooks checks the hooks: checker, when there is one, checks the
// block of each as it checks the build block of a component, and refuses a
// backend that runs no hook. A hook may name only a backend that runs
// hooks, so one checker does not know is an error.
func (m *Manifest) validateHooks(checker BuildChecker) ([]string, error) {
	var warnings []string

	for _, name := range sortedKeys(m.Hooks) {
		if !isHookName(name) {
			warnings = append(warnings, fmt.Sprintf(
				"unknown hook %q skipped (only pre_build, post_build and clean are supported)",
				name))

			continue
		}

		// A hook naming no backend was given shell when it was parsed
		// (applyHookDefaults).
		field := "hooks." + name
		hook := m.Hooks[name]

		err := validateOutput(field+".output", hook.Output)
		if err != nil {
			return warnings, err
		}

		if checker != nil {
			check, hookWarnings, err := m.checkBuild(checker, field, m.HookTarget(), hook)

			warnings = append(warnings, hookWarnings...)

			if err == nil && !check.Known {
				err = invalid(field, "no module of this tt provides backend %q", hook.Backend)
			}

			if err != nil {
				return warnings, err
			}
		}

		// A hook has no TT_OUTPUT_DIR, so the build knows nothing it made to
		// check, and static = true would promise what nothing checks.
		if hook.Static != nil {
			return warnings, invalid("hooks."+name+".static",
				"is for the build of a component: a hook makes nothing the build checks")
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
