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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ManifestVersion is the format version this build of tt writes and natively
// understands. The string is "<major>.<minor>".
const ManifestVersion = "0.1"

// Manifest is the parsed form of app.manifest.toml.
//
// The raw bytes of the source file are kept so manifest_hash can be computed
// without re-reading the file; any change to the file (including comments and
// formatting) changes the hash.
type Manifest struct {
	ManifestVersion string                `toml:"manifest_version"`
	Package         Package               `toml:"package"`
	Platform        Platform              `toml:"platform"`
	Dependencies    map[string]Dependency `toml:"dependencies,omitempty"`
	DevDependencies map[string]Dependency `toml:"dev_dependencies,omitempty"`
	Components      map[string]Component  `toml:"components,omitempty"`
	Products        map[string]Product    `toml:"products,omitempty"`
	Hooks           map[string]Hook       `toml:"hooks,omitempty"` // Keys: pre_build, post_build.

	raw []byte // Raw file bytes, source for manifest_hash.
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
// The returned warnings are non-fatal diagnostics for the caller to surface.
func ParseManifest(data []byte) (*Manifest, []string, error) {
	declared, err := peekManifestVersion(data)
	if err != nil {
		return nil, nil, err
	}

	// A different major in either direction is refused: a newer major may have
	// changed the format, and no older major exists for 0.x to fall back to.
	if declared.major != ourManifestVersion.major {
		return nil, nil, fmt.Errorf("manifest version %q %w (supports %d.x)",
			declared, ErrUnsupportedVersion, ourManifestVersion.major)
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

	out.raw = append([]byte(nil), data...)

	return out, warnings, nil
}

// handleDecodeError classifies a strict-decode failure. An unknown field is a
// warning when the manifest is a newer minor version and a hard error
// otherwise; any other decode failure is returned wrapped.
func handleDecodeError(decErr error, declared formatVersion) ([]string, error) {
	var strict *toml.StrictMissingError
	if !errors.As(decErr, &strict) {
		return nil, fmt.Errorf("parsing manifest: %w", decErr)
	}

	unknown := unknownKeys(strict)
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

// Marshal serializes the manifest back to TOML. Output is canonical go-toml
// form; comments and original formatting are not preserved.
func (m *Manifest) Marshal() ([]byte, error) {
	data, err := toml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}

	return data, nil
}

// Hash returns the manifest_hash: SHA-256 of the raw source bytes, tagged
// "sha256:". It is computed over the bytes ParseManifest was given.
func (m *Manifest) Hash() string {
	return HashBytes(m.raw)
}

// Raw returns the raw source bytes the manifest was parsed from.
func (m *Manifest) Raw() []byte {
	return m.raw
}

// HashBytes returns the "sha256:<hex>" hash of arbitrary manifest bytes.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)

	return "sha256:" + hex.EncodeToString(sum[:])
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

// unknownKeys flattens StrictMissingError into dotted key paths.
func unknownKeys(strict *toml.StrictMissingError) []string {
	keys := make([]string, 0, len(strict.Errors))
	for _, e := range strict.Errors {
		keys = append(keys, strings.Join(e.Key(), "."))
	}

	return keys
}
