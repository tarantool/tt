package manifest

import (
	"fmt"
	"regexp"
	"sort"
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

	return m.validatePlatforms()
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

		if comp.Build != nil {
			err := validateBuild(field+".build", *comp.Build)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

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
	case "":
		return invalid(field, "backend is required")
	default:
		return invalid(field,
			"backend %q is not supported in manifest_version %q (want make, shell, c or lua-c)",
			build.Backend, ManifestVersion)
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
