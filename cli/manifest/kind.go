package manifest

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// Manifest kinds, the values of manifest_kind. A manifest without the field
// is an application manifest, so app.manifest.toml files written before the
// field existed need no change.
const (
	// ManifestKindApp is an application package: app.manifest.toml, built with
	// tt package build.
	ManifestKindApp = "app"
	// ManifestKindDistribution is a distribution of tt: tt.manifest.toml, the core
	// and the tt modules a tt binary is built from with
	// tt binary build tt.
	ManifestKindDistribution = "tt-distribution"
)

// peekKind reads only manifest_kind, leniently, and returns the kind it
// names: ManifestKindApp when the field is absent. An unknown kind is an error
// whatever the format version, since the kind selects how the rest of the
// document is read.
func peekKind(data []byte) (string, error) {
	var head struct {
		Kind string `toml:"manifest_kind"`
	}

	err := toml.Unmarshal(data, &head)
	if err != nil {
		return "", fmt.Errorf("parsing manifest: %w", err)
	}

	switch head.Kind {
	case "", ManifestKindApp:
		return ManifestKindApp, nil
	case ManifestKindDistribution:
		return ManifestKindDistribution, nil
	default:
		return "", invalid("manifest_kind", "unknown kind %q: want %q or %q",
			head.Kind, ManifestKindApp, ManifestKindDistribution)
	}
}
