package manifest

import (
	"bytes"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// The files of a distribution of tt, next to each other in its directory.
const (
	// DistributionFile is the manifest of a distribution.
	DistributionFile = "tt.manifest.toml"
	// DistributionLockFile pins what a distribution was last built from.
	DistributionLockFile = "tt.manifest.lock"
	// DistributionSumFile is the go.sum of the last build of a
	// distribution: the checksums of every Go module it was built from.
	DistributionSumFile = "tt.manifest.sum"
)

// Defaults of a distribution manifest.
const (
	// DefaultDistributionName is the name of the binary a distribution
	// manifest without [package].name builds.
	DefaultDistributionName = "tt"
	// DefaultCoreModule is the Go module of the tt core.
	DefaultCoreModule = "github.com/tarantool/tt/v3"
	// DefaultModuleSymbol is the constructor a tt module exports.
	DefaultModuleSymbol = "New"
	// LatestVersion is the version query a module outside the core's
	// repository is resolved with when the manifest names no version.
	LatestVersion = "latest"
)

// Integrity providers ([distribution].integrity).
const (
	// IntegrityNone builds tt without integrity checks: the core's default.
	IntegrityNone = "none"
	// IntegrityRSA builds tt with the RSA provider of sdk/integrity.
	IntegrityRSA = "rsa"
)

// TLS implementations ([build].ssl); the same names TT_CLI_BUILD_SSL takes.
const (
	SSLDefault       = "default"
	SSLOpenSSL       = "openssl"
	SSLOpenSSLStatic = "openssl_static"
)

// ModuleKind is the only kind of a dependency of a distribution: a tt
// module, a Go module compiled into the binary.
const ModuleKind = "tt-module"

// Distribution is the parsed form of tt.manifest.toml: a distribution of tt,
// the core and the tt modules one tt binary is built from.
//
// Only manifest_version and manifest_kind are required; every table and
// every field in them has a default, so the smallest distribution manifest
// builds tt itself, from the core of the tt that builds it.
type Distribution struct {
	ManifestVersion string                      `toml:"manifest_version"`
	Kind            string                      `toml:"manifest_kind"`
	Package         DistributionPackage         `toml:"package,omitempty"`
	Core            DistributionCore            `toml:"core,omitempty"`
	Distribution    DistributionOptions         `toml:"distribution,omitempty"`
	Build           DistributionBuild           `toml:"build,omitempty"`
	Dependencies    map[string]ModuleDependency `toml:"dependencies,omitempty"`

	raw []byte // Raw file bytes, source for manifest_hash.
}

// DistributionPackage is [package] of a distribution.
type DistributionPackage struct {
	// Name is the name of the binary. Default: DefaultDistributionName.
	Name string `toml:"name,omitempty"`
}

// DistributionCore is [core]: the tt core the binary is built on.
type DistributionCore struct {
	// Tt is a Go version query for the core module: latest, a branch, a
	// commit, a version. Default: the version of the core of the tt that
	// builds the distribution.
	Tt string `toml:"tt,omitempty"`
	// Path is a local checkout of the core's repository, relative to the
	// manifest's directory. It excludes Tt.
	Path string `toml:"path,omitempty"`
	// Module is the Go module of the core. Default: DefaultCoreModule.
	Module string `toml:"module,omitempty"`
}

// DistributionOptions is [distribution]: how the binary presents itself and
// what the core does beyond its modules.
type DistributionOptions struct {
	// Title is the name the binary presents itself under. Default: tt's
	// own.
	Title string `toml:"title,omitempty"`
	// Edition follows the short version as semver build metadata: "ee"
	// makes it 3.1.0+ee. Default: none.
	Edition string `toml:"edition,omitempty"`
	// Integrity is the integrity provider: IntegrityNone or IntegrityRSA.
	// Default: IntegrityNone.
	Integrity string `toml:"integrity,omitempty"`
	// Builtin includes tt's own commands. Default: true.
	Builtin *bool `toml:"builtin,omitempty"`
	// ExternalModules lets tt run the external modules TT_CLI_MODULES_PATH
	// lists. Default: true.
	ExternalModules *bool `toml:"external_modules,omitempty"`
}

// DistributionBuild is [build]: how the binary is compiled.
type DistributionBuild struct {
	// SSL is the TLS implementation: SSLDefault, SSLOpenSSL or
	// SSLOpenSSLStatic. TT_CLI_BUILD_SSL, when set, takes precedence.
	// Default: SSLDefault.
	SSL string `toml:"ssl,omitempty"`
	// Cgo builds with cgo. Default: only for the OpenSSL implementations.
	Cgo *bool `toml:"cgo,omitempty"`
	// Tags are build tags added to the canonical ones.
	Tags []string `toml:"tags,omitempty"`
	// Ldflags are linker flags added to the canonical ones, one flag per
	// entry.
	Ldflags []string `toml:"ldflags,omitempty"`
}

// ModuleDependency is one tt module of a distribution
// ([dependencies.<name>]): a Go module whose package exports a constructor
// of tt commands.
//
// It is written in two forms. The short form is a string: a local path
// ("./plugins/migrations": it starts with ./, ../ or /), a module path
// ("github.com/tarantool/tt/modules/aeon") or a module path with a version
// query ("github.com/acme/tt-foo@v1.2.0"). The long form is a table with the
// fields below.
type ModuleDependency struct {
	// Module is the Go module path. Required unless Path is set; for a path
	// dependency it is read from the go.mod there.
	Module string `toml:"module,omitempty"`
	// Version is a Go version query. Default: the core's commit for a
	// module of the core's repository, LatestVersion for any other.
	Version string `toml:"version,omitempty"`
	// Path is a local directory with the module, relative to the
	// manifest's directory.
	Path string `toml:"path,omitempty"`
	// Package is the import path of the package with the constructor.
	// Default: the module's root package.
	Package string `toml:"package,omitempty"`
	// Symbol is the constructor's name. Default: DefaultModuleSymbol.
	Symbol string `toml:"symbol,omitempty"`
	// Kind is ModuleKind, the only kind there is; it may be omitted.
	Kind string `toml:"kind,omitempty"`
	// Source is "registry" or "path"; it follows from Path when omitted.
	Source string `toml:"source,omitempty"`
}

// ParseDistribution parses tt.manifest.toml bytes into a Distribution.
//
// The format-version rules are those of ParseManifest: an unknown field is a
// warning in a manifest of a newer minor version and an error otherwise, a
// different major version is refused. A manifest whose manifest_kind is not
// ManifestKindDistribution is refused with an error that names the command it
// is for. Fields are checked by Validate.
func ParseDistribution(data []byte) (*Distribution, []string, error) {
	declared, err := peekManifestVersion(data)
	if err != nil {
		return nil, nil, err
	}

	if declared.major != ourManifestVersion.major {
		return nil, nil, invalidVersion(declared)
	}

	kind, err := peekKind(data)
	if err != nil {
		return nil, nil, err
	}

	if kind != ManifestKindDistribution {
		return nil, nil, invalid("manifest_kind",
			"want %q, a manifest without it is the manifest of an application: "+
				"build it with tt package build", ManifestKindDistribution)
	}

	out := new(Distribution)
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.EnableUnmarshalerInterface()
	dec.DisallowUnknownFields()

	var warnings []string

	decErr := dec.Decode(out)
	if decErr != nil {
		warnings, err = handleDecodeError(decErr, declared)
		if err != nil {
			return nil, nil, err
		}
	}

	for name, dep := range out.Dependencies {
		dep.applyDefaults()

		out.Dependencies[name] = dep
	}

	out.raw = append([]byte(nil), data...)

	return out, warnings, nil
}

// Hash returns the manifest_hash of the distribution manifest: the SHA-256
// of the bytes ParseDistribution was given.
func (d *Distribution) Hash() string {
	return HashBytes(d.raw)
}

// Name returns the name of the binary the distribution builds.
func (d *Distribution) Name() string {
	if d.Package.Name == "" {
		return DefaultDistributionName
	}

	return d.Package.Name
}

// CoreModule returns the Go module of the core.
func (d *Distribution) CoreModule() string {
	if d.Core.Module == "" {
		return DefaultCoreModule
	}

	return d.Core.Module
}

// IntegrityValue returns the integrity provider, IntegrityNone by default.
func (o DistributionOptions) IntegrityValue() string {
	if o.Integrity == "" {
		return IntegrityNone
	}

	return o.Integrity
}

// BuiltinValue reports whether tt's own commands are included.
func (o DistributionOptions) BuiltinValue() bool {
	return o.Builtin == nil || *o.Builtin
}

// ExternalModulesValue reports whether tt runs external modules.
func (o DistributionOptions) ExternalModulesValue() bool {
	return o.ExternalModules == nil || *o.ExternalModules
}

// IsPath reports whether the module is a local directory.
func (m *ModuleDependency) IsPath() bool {
	return m.Source == sourcePath
}

// SymbolValue returns the name of the constructor.
func (m *ModuleDependency) SymbolValue() string {
	if m.Symbol == "" {
		return DefaultModuleSymbol
	}

	return m.Symbol
}

// UnmarshalTOML decodes a module dependency from its short form, a string,
// or its long form, an inline table. A standard sub-table is decoded by the
// decoder field by field and defaulted by ParseDistribution.
func (m *ModuleDependency) UnmarshalTOML(node *unstable.Node) error {
	switch node.Kind {
	case unstable.String:
		return m.parseShort(string(node.Data))
	case unstable.InlineTable, unstable.Table:
		return m.unmarshalTable(node)
	default:
		return invalid("", "a tt module must be a string or a table, got %s", node.Kind)
	}
}

// parseShort decodes the short form: a local path, a module path or a module
// path with a version query after @.
func (m *ModuleDependency) parseShort(value string) error {
	switch {
	case isLocalPath(value):
		m.Path = value
	case strings.Contains(value, "@"):
		module, version, _ := strings.Cut(value, "@")
		if module == "" || version == "" {
			return invalid("", "%q: want <module>@<version>", value)
		}

		m.Module = module
		m.Version = version
	default:
		m.Module = value
	}

	m.applyDefaults()

	return nil
}

// unmarshalTable decodes the long form written as an inline table.
func (m *ModuleDependency) unmarshalTable(node *unstable.Node) error {
	it := node.Children()
	for it.Next() {
		entry := it.Node()

		keyIter := entry.Key()
		if !keyIter.Next() {
			continue
		}

		key := string(keyIter.Node().Data)

		value := entry.Value()
		if value.Kind != unstable.String {
			return invalid("", "tt module field %q must be a string", key)
		}

		err := m.setField(key, string(value.Data))
		if err != nil {
			return err
		}
	}

	m.applyDefaults()

	return nil
}

// setField assigns one field of the long form by its TOML key.
func (m *ModuleDependency) setField(key, value string) error {
	fields := map[string]*string{
		"module":  &m.Module,
		"version": &m.Version,
		"path":    &m.Path,
		"package": &m.Package,
		"symbol":  &m.Symbol,
		"kind":    &m.Kind,
		"source":  &m.Source,
	}

	field, ok := fields[key]
	if !ok {
		return invalid("", "unknown tt module field %q", key)
	}

	*field = value

	return nil
}

// applyDefaults derives the source from the path when it is not written.
func (m *ModuleDependency) applyDefaults() {
	if m.Source != "" {
		return
	}

	if m.Path != "" {
		m.Source = sourcePath
	} else {
		m.Source = sourceRegistry
	}
}

// isLocalPath reports whether the short form of a module is a local path.
func isLocalPath(value string) bool {
	return value == "." || value == ".." ||
		strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") ||
		strings.HasPrefix(value, "/")
}
