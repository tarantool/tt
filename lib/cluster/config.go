package cluster

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	fmtPathNotMap = "path %q is not a map"
)

// Config is a container for deserialized configuration.
type Config struct {
	// paths is a container implementation for the deserialized configuration.
	//
	// At this moment it is fully-compatible with yaml.v3 Marshal()/Unmarshal()
	// functions. So it could be marshaled and unmarshaled directly to YAML.
	// We may change it in the future.
	paths any
}

// NewConfig creates a new empty configuration.
func NewConfig() *Config {
	return &Config{}
}

// deepCopyMap creates a deep copy of map[any]any object if the value is
// a map of the type.
func deepCopyMap(value any) any {
	if srcMap, ok := value.(map[any]any); ok {
		copyMap := make(map[any]any)
		for k, v := range srcMap {
			copyMap[k] = deepCopyMap(v)
		}
		return copyMap
	} else {
		return value
	}
}

// createMaps creates or ensures that map[any]any sequence exists for the path.
// It returns a last map in the path or an error.
func (config *Config) createMaps(path []string) (map[any]any, error) {
	var (
		prev         map[any]any
		prevKey      string
		currentValue any
	)

	currentValue = config.paths
	for i := 0; i < len(path)+1; i++ {
		if currentValue == nil {
			currentValue = make(map[any]any)
		} else if _, ok := currentValue.(map[any]any); !ok {
			return nil, fmt.Errorf(fmtPathNotMap, path[0:i])
		}

		if prev == nil {
			config.paths = currentValue
		} else {
			prev[prevKey] = currentValue
		}

		if i < len(path) {
			prev = currentValue.(map[any]any)
			prevKey = path[i]
			currentValue = prev[prevKey]
		}
	}
	return currentValue.(map[any]any), nil
}

// getMap returns a map for the path.
func (config *Config) getMap(path []string) (map[any]any, error) {
	currentValue := config.paths
	for i := 0; i < len(path)+1; i++ {
		if m, ok := currentValue.(map[any]any); !ok {
			return nil, fmt.Errorf(fmtPathNotMap, path[0:i])
		} else if i < len(path) {
			if _, ok := m[path[i]]; !ok {
				return nil, NotExistError{path[0 : i+1]}
			} else {
				currentValue = m[path[i]]
			}
		}
	}

	return currentValue.(map[any]any), nil
}

// Set sets a value to a configuration path.
func (config *Config) Set(path []string, value any) error {
	if path == nil || len(path) == 0 {
		config.paths = value
		return nil
	}

	last := len(path) - 1
	if m, err := config.createMaps(path[0:last]); err != nil {
		return err
	} else {
		key := path[last]
		if cfg, ok := value.(*Config); value != nil && ok {
			m[key] = deepCopyMap(cfg.paths)
		} else {
			m[key] = value
		}
	}

	return nil
}

// Get returns a value from a configuration path.
func (config *Config) Get(path []string) (any, error) {
	if path == nil || len(path) == 0 {
		if config.paths == nil {
			return nil, NotExistError{path}
		}
		return config.paths, nil
	}

	last := len(path) - 1
	if m, err := config.getMap(path[0:last]); err != nil {
		return nil, err
	} else {
		ret := m[path[last]]
		if ret == nil {
			return nil, NotExistError{path}
		}
		return ret, nil
	}
}

// Elems returns a list of an elements for a path.
func (config *Config) Elems(path []string) ([]string, error) {
	var target map[any]any
	if path == nil || len(path) == 0 {
		if config.paths == nil {
			return nil, NotExistError{path}
		}

		if m, ok := config.paths.(map[any]any); !ok {
			return nil, fmt.Errorf(fmtPathNotMap, path)
		} else {
			target = m
		}
	} else if m, err := config.getMap(path); err != nil {
		return nil, err
	} else {
		target = m
	}

	result := []string{}
	for k := range target {
		if str, ok := k.(string); ok {
			result = append(result, str)
		}
	}
	return result, nil
}

// forEachMap iterates overs maps recursively.
func forEachMap(path []string, dst map[any]any, fun func([]string, any)) {
	if len(dst) == 0 {
		// It is a value itself.
		fun(path, dst)
	} else {
		for k, v := range dst {
			if str, ok := k.(string); ok {
				if m, ok := v.(map[any]any); ok {
					forEachMap(append(path, str), m, fun)
				} else if v != nil {
					fun(append(path, str), v)
				}
			}
		}
	}
}

// ForEach iterates over each value in the configuration.
func (config *Config) ForEach(path []string, fun func(path []string, value any)) {
	var target any
	if path == nil || len(path) == 0 {
		if config.paths == nil {
			return
		} else {
			target = config.paths
		}
	} else {
		last := len(path) - 1
		if m, err := config.getMap(path[0:last]); err != nil {
			return
		} else {
			target = m[path[last]]
		}
	}

	if m, ok := target.(map[any]any); ok {
		forEachMap(path, m, fun)
	} else {
		fun(path, target)
	}
}

// Merge merges a configuration to the current. The outside configuration has
// a low priority.
func (config *Config) Merge(low *Config) {
	low.ForEach(nil, func(path []string, value any) {
		if _, err := config.Get(path); err != nil {
			config.Set(path, value)
		}
	})
}

// deepCastStringMapToAnyMap casts all map[string]any to map[any]any deeply.
func deepCastStringMapToAnyMap(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[any]any{}
		for k, v2 := range x {
			m[k] = deepCastStringMapToAnyMap(v2)
		}
		v = m
	case map[any]any:
		for k, v2 := range x {
			x[k] = deepCastStringMapToAnyMap(v2)
		}
	case []any:
		for i, v2 := range x {
			x[i] = deepCastStringMapToAnyMap(v2)
		}
	default:
	}
	return v
}

// UnmarshalYAML helps to unmarshal the configuration from a YAML document.
func (config *Config) UnmarshalYAML(unmarshal func(any) error) error {
	var rawPaths any
	if err := unmarshal(&rawPaths); err != nil {
		return fmt.Errorf("failed to unmarshal Config: %w", err)
	}
	config.paths = deepCastStringMapToAnyMap(rawPaths)
	return nil
}

// String returns a string representation of the configuration. Actually it is
// a valid YAML.
func (config *Config) String() string {
	if config.paths == nil {
		return ""
	}

	sb := &strings.Builder{}
	encoder := yaml.NewEncoder(sb)
	encoder.SetIndent(2)
	if err := encoder.Encode(config.paths); err != nil {
		panic(fmt.Sprintf("failed to marshal a config: %s", err))
	}

	return sb.String()
}
