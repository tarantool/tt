package manifest

import "github.com/pelletier/go-toml/v2/unstable"

// Source values for a dependency.
const (
	sourceRegistry = "registry"
	sourcePath     = "path"
)

// Dependency is one entry of a [dependencies]/[dev_dependencies] map or a
// component's [components.<name>.dependencies] map.
//
// It is written in two forms. The short form is a bare constraint string
// (luasocket = ">=3.0.0,<4.0.0"), where source defaults to "registry". The
// long form is a table with source/version/path/registry. Both forms occur in
// the same map, so the dual-form decoding lives in UnmarshalTOML.
type Dependency struct {
	Source   string `toml:"source,omitempty"`   // Source: "registry" (default) or "path".
	Version  string `toml:"version,omitempty"`  // Constraint; required for registry.
	Path     string `toml:"path,omitempty"`     // Required for path.
	Registry string `toml:"registry,omitempty"` // Overrides the server.
	Kind     string `toml:"kind,omitempty"`     // In v0 only "library".
}

// UnmarshalTOML implements the go-toml unstable.Unmarshaler interface so a
// dependency can decode from either a bare string or a table. The decoder must
// have EnableUnmarshalerInterface set (ParseManifest/ParseLock do).
func (d *Dependency) UnmarshalTOML(node *unstable.Node) error {
	switch node.Kind {
	case unstable.String:
		// Short form: a bare constraint string.
		d.Source = sourceRegistry
		d.Version = string(node.Data)

		return nil
	case unstable.Table, unstable.InlineTable:
		return d.unmarshalTable(node)
	default:
		return invalid("", "dependency must be a constraint string or a table, got %s", node.Kind)
	}
}

// unmarshalTable decodes the long form: a table with source/version/path/
// registry/kind fields. An empty source defaults to "registry".
func (d *Dependency) unmarshalTable(node *unstable.Node) error {
	it := node.Children()
	for it.Next() {
		entry := it.Node()

		keyIter := entry.Key()
		if !keyIter.Next() {
			continue
		}

		key := string(keyIter.Node().Data)

		value := entry.Value()
		switch value.Kind {
		case unstable.String, unstable.Integer, unstable.Float, unstable.Bool:
		default:
			return invalid("", "dependency field %q must be a scalar value", key)
		}

		err := d.setField(key, string(value.Data))
		if err != nil {
			return err
		}
	}

	if d.Source == "" {
		d.Source = sourceRegistry
	}

	return nil
}

// setField assigns one decoded long-form field by its TOML key, rejecting
// unknown keys.
func (d *Dependency) setField(key, val string) error {
	switch key {
	case "source":
		d.Source = val
	case "version":
		d.Version = val
	case "path":
		d.Path = val
	case "registry":
		d.Registry = val
	case "kind":
		d.Kind = val
	default:
		return invalid("", "unknown dependency field %q", key)
	}

	return nil
}
