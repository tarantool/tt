package manifest

import (
	"go/token"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// builtinModule is the name the core's own commands are listed under in the
// table of modules; no tt module may take it while they are included.
const builtinModule = "builtin"

var (
	// binaryNameRe is the shape of the name of a binary: no separators, no
	// leading dot or dash.
	binaryNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// moduleNameRe is the shape of the name of a tt module: the key it is
	// listed under in [dependencies] and in the table of modules.
	moduleNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	// editionRe is the shape of semver build metadata.
	editionRe = regexp.MustCompile(`^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$`)
)

// Validate checks the distribution manifest structurally and reports the
// first error it finds, as a *ValidationError. Whether the core and the
// modules resolve, and whether a path module's go.mod matches what the
// manifest says about it, is checked by the build.
func (d *Distribution) Validate() error {
	checks := []func() error{
		d.validateVersion,
		d.validatePackage,
		d.validateCore,
		d.validateOptions,
		d.validateBuild,
		d.validateDependencies,
		d.validateReplace,
	}

	for _, check := range checks {
		err := check()
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Distribution) validateVersion() error {
	_, err := parseFormatVersion(d.ManifestVersion)
	if err != nil {
		return invalid("manifest_version", "%s", err)
	}

	return nil
}

func (d *Distribution) validatePackage() error {
	if d.Package.Name != "" && !binaryNameRe.MatchString(d.Package.Name) {
		return invalid("package.name", "%q is not a file name", d.Package.Name)
	}

	return nil
}

func (d *Distribution) validateCore() error {
	core := d.Core

	if core.Tt != "" && core.Path != "" {
		return invalid("core", "tt and path exclude each other")
	}

	if strings.ContainsFunc(core.Tt, unicode.IsSpace) {
		return invalid("core.tt", "%q is not a version query", core.Tt)
	}

	if core.Module != "" && !isModulePath(core.Module) {
		return invalid("core.module", "%q is not a Go module path", core.Module)
	}

	return nil
}

func (d *Distribution) validateOptions() error {
	opts := d.Distribution

	switch opts.Integrity {
	case "", IntegrityNone, IntegrityRSA:
	default:
		return invalid("distribution.integrity", "unknown provider %q: want %q or %q",
			opts.Integrity, IntegrityNone, IntegrityRSA)
	}

	if opts.Edition != "" && !editionRe.MatchString(opts.Edition) {
		return invalid("distribution.edition",
			"%q is not semver build metadata: dot-separated [0-9A-Za-z-] identifiers",
			opts.Edition)
	}

	return nil
}

func (d *Distribution) validateBuild() error {
	build := d.Build

	switch build.SSL {
	case "", SSLDefault, SSLOpenSSL, SSLOpenSSLStatic:
	default:
		return invalid("build.ssl", "unknown TLS implementation %q: want %q, %q or %q",
			build.SSL, SSLDefault, SSLOpenSSL, SSLOpenSSLStatic)
	}

	for _, tag := range build.Tags {
		if tag == "" || strings.ContainsFunc(tag, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		}) {
			return invalid("build.tags", "%q is not a build tag", tag)
		}
	}

	if slices.Contains(build.Ldflags, "") {
		return invalid("build.ldflags", "an empty flag")
	}

	return nil
}

func (d *Distribution) validateDependencies() error {
	for _, name := range sortedKeys(d.Dependencies) {
		field := "dependencies." + name

		if !moduleNameRe.MatchString(name) {
			return invalid(field,
				"a tt module name is a letter followed by letters, digits, - and _")
		}

		if name == builtinModule && d.Distribution.BuiltinValue() {
			return invalid(field, "%q names tt's own commands; "+
				"rename the module or set distribution.builtin = false", builtinModule)
		}

		dep := d.Dependencies[name]

		err := dep.validate(field)
		if err != nil {
			return err
		}
	}

	return nil
}

// validate checks one tt module, reporting errors against field.
func (m *ModuleDependency) validate(field string) error {
	if m.Kind != "" && m.Kind != ModuleKind {
		return invalid(field+".kind", "unknown kind %q: a distribution lists only %q",
			m.Kind, ModuleKind)
	}

	switch m.Source {
	case sourcePath:
		if m.Path == "" {
			return invalid(field+".path", "is required for a path module")
		}

		if m.Version != "" {
			return invalid(field+".version", "a path module has no version")
		}
	case sourceRegistry:
		if m.Path != "" {
			return invalid(field+".path", "a registry module has no path")
		}

		if m.Module == "" {
			return invalid(field+".module", "is required unless path is set")
		}

		if strings.ContainsFunc(m.Version, unicode.IsSpace) {
			return invalid(field+".version", "%q is not a version query", m.Version)
		}
	default:
		return invalid(field+".source", "unknown source %q: want %q or %q",
			m.Source, sourceRegistry, sourcePath)
	}

	return m.validateImport(field)
}

// validateImport checks the module path, the package and the symbol.
func (m *ModuleDependency) validateImport(field string) error {
	if m.Module != "" && !isModulePath(m.Module) {
		return invalid(field+".module", "%q is not a Go module path", m.Module)
	}

	if m.Package != "" {
		if !isModulePath(m.Package) {
			return invalid(field+".package", "%q is not a Go import path", m.Package)
		}

		if m.Module != "" && m.Package != m.Module &&
			!strings.HasPrefix(m.Package, m.Module+"/") {
			return invalid(field+".package", "%q is not a package of %q", m.Package, m.Module)
		}
	}

	if m.Symbol != "" && (!token.IsIdentifier(m.Symbol) || !token.IsExported(m.Symbol)) {
		return invalid(field+".symbol", "%q is not an exported Go identifier", m.Symbol)
	}

	return nil
}

// isModulePath reports whether path looks like a Go module or import path:
// slash-separated, with no empty, dot or space element. The go command
// checks the rest.
func isModulePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}

	for elem := range strings.SplitSeq(path, "/") {
		if elem == "" || elem == "." || elem == ".." ||
			strings.ContainsFunc(elem, unicode.IsSpace) {
			return false
		}
	}

	return true
}
