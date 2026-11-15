// Package manifest is the data layer for tt packages: Go types for the
// manifest (app.manifest.toml) and the lock (app.manifest.lock), TOML reading
// and writing, structural validation and format-version handling.
//
// It does no git, no dependency resolution and never touches .rocks/. Every
// other package of the tt manifest pipeline imports it, so its only
// dependencies are the standard library and the TOML library.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ManifestVersion is the format version this build of tt writes and natively
// understands. The string is "<major>.<minor>".
const ManifestVersion = "0.1"

// Manifest is the parsed form of app.manifest.toml.
//
// Its manifest_hash is computed when it is parsed, over the data of the file
// and not over its bytes, so comments and formatting do not change it (see
// HashTOML). The bytes are kept as well, for a package to carry them as they
// are.
type Manifest struct {
	ManifestVersion string                `toml:"manifest_version"`
	Kind            string                `toml:"manifest_kind,omitempty"` // "" or ManifestKindApp.
	Package         Package               `toml:"package"`
	Platform        Platform              `toml:"platform"`
	Dependencies    map[string]Dependency `toml:"dependencies,omitempty"`
	DevDependencies map[string]Dependency `toml:"dev_dependencies,omitempty"`
	Components      map[string]Component  `toml:"components,omitempty"`
	Products        map[string]Product    `toml:"products,omitempty"`
	Hooks           map[string]Hook       `toml:"hooks,omitempty"` // pre_build, post_build, clean.

	raw  []byte // Raw file bytes.
	hash string // manifest_hash of raw, see HashTOML.
}

// Package holds the package identity and metadata ([package]).
type Package struct {
	Name               string   `toml:"name"` // Regex [a-z][a-z0-9-]*; not "bin"/"manifests".
	Description        string   `toml:"description,omitempty"`
	License            string   `toml:"license,omitempty"`
	LicenseFiles       []string `toml:"license_files,omitempty"`
	Include            []string `toml:"include,omitempty"`
	Repository         string   `toml:"repository,omitempty"`
	Authors            []string `toml:"authors,omitempty"`
	GenerateVersionLua *bool    `toml:"generate_version_lua,omitempty"` // Nil means true.
}

// Platform describes the runtime requirements ([platform]).
//
// The version constraints are stored already parsed: the semver part and the
// flavor are kept separately so [ce]/[ee] is not parsed twice down the line.
type Platform struct {
	Tarantool Constraint `toml:"tarantool"`           // Required; flavor [ce]/[ee], default [ce].
	Tt        Constraint `toml:"tt"`                  // Required.
	Tcm       Constraint `toml:"tcm,omitempty"`       // No flavor - TCM is Enterprise only.
	Platforms []string   `toml:"platforms,omitempty"` // One of linux/darwin-amd64/arm64 or any.

	// Registries is the ordered rock-server list the project resolves
	// against, replacing the built-in defaults. An entry is an HTTP(S) URL
	// or a local directory - a path without a scheme, or a file:// URL - and
	// a relative path is taken relative to the directory holding the
	// manifest. Omitted uses the defaults.
	Registries []string `toml:"registries,omitempty"`
}

// GenerateVersionLuaValue reports the effective value of
// package.generate_version_lua, defaulting to true when unset.
func (p Package) GenerateVersionLuaValue() bool {
	return p.GenerateVersionLua == nil || *p.GenerateVersionLua
}

// ParseManifest parses app.manifest.toml bytes into a Manifest.
//
// It applies the format-version rules to unknown fields: an unknown field in a
// manifest of a newer minor version produces a warning and parsing continues;
// in the current or an older minor version it is a hard error; a newer major
// version is refused outright. Unknown values of known enum fields are not
// handled here - they are caught by Validate regardless of format version.
//
// The fields of a build block but the common ones (Build) go to Build.Rest
// unchecked: which of them a backend has, Validate tells, with the backends
// of the tt it is given, by the same format-version rules.
//
// A manifest of another kind (manifest_kind) is refused with an error that
// names the command it is for.
//
// The returned warnings are non-fatal diagnostics for the caller to surface.
func ParseManifest(data []byte) (*Manifest, []string, error) {
	declared, err := peekManifestVersion(data)
	if err != nil {
		return nil, nil, err
	}

	kind, err := peekKind(data)
	if err != nil {
		return nil, nil, err
	}

	if kind != ManifestKindApp {
		return nil, nil, invalid("manifest_kind",
			"%q is a manifest of a tt distribution, not of an application: "+
				"build it with tt binary build tt", kind)
	}

	// A different major in either direction is refused: a newer major may have
	// changed the format, and no older major exists for 0.x to fall back to.
	if declared.major != ourManifestVersion.major {
		return nil, nil, invalidVersion(declared)
	}

	out := new(Manifest)
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

	out.applyDependencyDefaults()
	out.applyHookDefaults()

	// The generic tree of the document is decoded once: manifest_hash is
	// taken over it, and the fields of the backends of the build blocks are
	// read from it, which the strict decode above left out.
	tree, err := parseTree(data)
	if err != nil {
		return nil, nil, err
	}

	out.hash, err = hashTree(tree)
	if err != nil {
		return nil, nil, err
	}

	out.fillRest(tree)

	out.raw = append([]byte(nil), data...)

	return out, warnings, nil
}

// handleDecodeError classifies a strict-decode failure. An unknown field is a
// warning when the manifest is a newer minor version and a hard error
// otherwise; any other decode failure is returned wrapped. A field of a build
// block is not unknown here: it goes to Build.Rest (isBuildField).
func handleDecodeError(decErr error, declared formatVersion) ([]string, error) {
	var strict *toml.StrictMissingError
	if !errors.As(decErr, &strict) {
		return nil, fmt.Errorf("parsing manifest: %w", decErr)
	}

	unknown := unknownKeys(strict)
	if len(unknown) == 0 {
		return nil, nil
	}

	if !declared.minorNewerThan(ourManifestVersion) {
		return nil, invalid(unknown[0], "unknown field in manifest version %q", declared)
	}

	warnings := make([]string, 0, len(unknown))
	for _, k := range unknown {
		warnings = append(warnings, fmt.Sprintf(
			"field %q appeared in a newer manifest version; trying to ignore it", k))
	}

	return warnings, nil
}

// Hash returns the manifest_hash a lock resolved from the manifest records,
// computed over the document ParseManifest was given: see HashTOML. It is
// empty for a Manifest that was not parsed.
func (m *Manifest) Hash() string {
	return m.hash
}

// CheckHash reports whether recorded, the manifest_hash of a lock, names this
// manifest: nil when it does; ErrManifestChanged when the manifest changed
// since the lock was written; ErrHashOutdated when an older tt, which hashed
// the manifest another way, wrote the lock; ErrHashScheme when the value is of
// a scheme this tt does not know.
func (m *Manifest) CheckHash(recorded string) error {
	return checkHash(recorded, m.hash)
}

// Raw returns the raw source bytes the manifest was parsed from.
func (m *Manifest) Raw() []byte {
	return m.raw
}

// Marshal serializes the manifest back to TOML. Output is canonical go-toml
// form; comments and original formatting are not preserved.
func (m *Manifest) Marshal() ([]byte, error) {
	data, err := toml.Marshal(m.toWire())
	if err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}

	return data, nil
}

// manifestWire is the form a Manifest is written in: a build block is a
// table of its fields and those of its Rest, which the encoder cannot put
// together from the Build struct.
type manifestWire struct {
	ManifestVersion string                    `toml:"manifest_version"`
	Kind            string                    `toml:"manifest_kind,omitempty"`
	Package         Package                   `toml:"package"`
	Platform        Platform                  `toml:"platform"`
	Dependencies    map[string]Dependency     `toml:"dependencies,omitempty"`
	DevDependencies map[string]Dependency     `toml:"dev_dependencies,omitempty"`
	Components      map[string]componentWire  `toml:"components,omitempty"`
	Products        map[string]Product        `toml:"products,omitempty"`
	Hooks           map[string]map[string]any `toml:"hooks,omitempty"`
}

// componentWire is the form a Component is written in.
type componentWire struct {
	Path         string                `toml:"path"`
	Kind         string                `toml:"kind,omitempty"`
	Include      []string              `toml:"include,omitempty"`
	Exclude      []string              `toml:"exclude,omitempty"`
	Namespace    *string               `toml:"namespace,omitempty"`
	Dependencies map[string]Dependency `toml:"dependencies,omitempty"`
	Build        map[string]any        `toml:"build,omitempty"`
}

// toWire returns the form m is written in.
func (m *Manifest) toWire() manifestWire {
	var components map[string]componentWire

	if m.Components != nil {
		components = make(map[string]componentWire, len(m.Components))
	}

	for name, component := range m.Components {
		var build map[string]any

		if component.Build != nil {
			build = component.Build.tree()
		}

		components[name] = componentWire{
			Path:         component.Path,
			Kind:         component.Kind,
			Include:      component.Include,
			Exclude:      component.Exclude,
			Namespace:    component.Namespace,
			Dependencies: component.Dependencies,
			Build:        build,
		}
	}

	var hooks map[string]map[string]any

	if m.Hooks != nil {
		hooks = make(map[string]map[string]any, len(m.Hooks))
	}

	for name, hook := range m.Hooks {
		hooks[name] = hook.tree()
	}

	return manifestWire{
		ManifestVersion: m.ManifestVersion,
		Kind:            m.Kind,
		Package:         m.Package,
		Platform:        m.Platform,
		Dependencies:    m.Dependencies,
		DevDependencies: m.DevDependencies,
		Components:      components,
		Products:        m.Products,
		Hooks:           hooks,
	}
}

// fillRest puts the fields of every build block of m but the common ones,
// which the strict decode left out, into its Rest, from tree, the generic
// tree of the document m was decoded from.
func (m *Manifest) fillRest(tree map[string]any) {
	components, _ := tree["components"].(map[string]any)

	for name, component := range m.Components {
		if component.Build == nil {
			continue
		}

		table, _ := components[name].(map[string]any)
		block, _ := table["build"].(map[string]any)

		component.Build.Rest = restOf(block)
	}

	hooks, _ := tree["hooks"].(map[string]any)

	for name, hook := range m.Hooks {
		block, _ := hooks[name].(map[string]any)

		hook.Rest = restOf(block)
		m.Hooks[name] = hook
	}
}

// restOf returns the fields of a build block but the common ones; nil when
// there are none.
func restOf(block map[string]any) map[string]any {
	var rest map[string]any

	for key, value := range block {
		if slices.Contains(commonBuildFields, key) {
			continue
		}

		if rest == nil {
			rest = map[string]any{}
		}

		rest[key] = value
	}

	return rest
}

// isBuildField reports whether key, the path of a field the strict decode
// did not know, is in a build block: [components.<name>.build] or
// [hooks.<name>]. Such a field is one of the backend of the block, which
// goes to Build.Rest.
func isBuildField(key []string) bool {
	switch {
	case len(key) >= 4 && key[0] == "components" && key[2] == "build":
		return true
	case len(key) >= 3 && key[0] == "hooks":
		return true
	default:
		return false
	}
}

// applyDependencyDefaults defaults every declared dependency, in the global
// tables and in each component's, so a dependency reads the same whichever
// TOML form it was written in. See Dependency.applyDefaults for why the
// decoder alone does not get this right.
func (m *Manifest) applyDependencyDefaults() {
	defaults := func(deps map[string]Dependency) {
		for name, dep := range deps {
			dep.applyDefaults()

			deps[name] = dep
		}
	}

	defaults(m.Dependencies)
	defaults(m.DevDependencies)

	for _, component := range m.Components {
		defaults(component.Dependencies)
	}
}

// DefaultHookBackend is the backend of a hook that names none, as the format
// of the manifest has it. A module provides it, the standard module build; a
// tt without one refuses such a hook, saying that no module provides a
// backend that runs hooks.
const DefaultHookBackend = "shell"

// applyHookDefaults gives a hook that names no backend the default one
// (DefaultHookBackend). Validate checks the hook with it and the build runs
// the hook with it: a default Validate alone assumed let a manifest through
// that the build then refused to run.
func (m *Manifest) applyHookDefaults() {
	for name, hook := range m.Hooks {
		if hook.Backend == "" {
			hook.Backend = DefaultHookBackend
			m.Hooks[name] = hook
		}
	}
}

// invalidVersion reports a manifest_version of a major this tt does not
// read.
func invalidVersion(declared formatVersion) error {
	return fmt.Errorf("manifest version %q %w (supports %d.x)",
		declared, ErrUnsupportedVersion, ourManifestVersion.major)
}

// peekManifestVersion reads only the manifest_version field, leniently.
func peekManifestVersion(data []byte) (formatVersion, error) {
	var head struct {
		ManifestVersion string `toml:"manifest_version"`
	}

	err := toml.Unmarshal(data, &head)
	if err != nil {
		return formatVersion{}, fmt.Errorf("parsing manifest: %w", err)
	}

	if head.ManifestVersion == "" {
		return formatVersion{}, invalid("manifest_version", "is required")
	}

	return parseFormatVersion(head.ManifestVersion)
}

// unknownKeys flattens StrictMissingError into dotted key paths, leaving out
// the fields of build blocks (isBuildField).
func unknownKeys(strict *toml.StrictMissingError) []string {
	keys := make([]string, 0, len(strict.Errors))
	for _, e := range strict.Errors {
		if isBuildField(e.Key()) {
			continue
		}

		keys = append(keys, strings.Join(e.Key(), "."))
	}

	return keys
}
