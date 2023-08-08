package cluster

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

const (
	fmtPathNotMap   = "path %q is not a map"
	fmtPathNotExist = "path %q does not exist"
)

// Config is a container for deserialized configuration.
type Config struct {
	// paths is a container implementation for the deserialized configuration.
	//
	// At this moment it is fully-compatible with yaml.v2 Marshal()/Unmarshal()
	// functions. So it could be marshaled and unmarshaled directly to YAML.
	// We may change it in the future.
	paths any
}

// NewConfig creates a new empty configuration.
func NewConfig() *Config {
	return &Config{}
}

// createMaps creates or ensures that map[any]any sequence exists for the path.
// It returns a last map in the path or an error.
func (c *Config) createMaps(path []string) (map[any]any, error) {
	var (
		prev         map[any]any
		prevKey      string
		currentValue any
	)

	currentValue = c.paths
	for i := 0; i < len(path)+1; i++ {
		if currentValue == nil {
			currentValue = make(map[any]any)
		} else if _, ok := currentValue.(map[any]any); !ok {
			return nil, fmt.Errorf(fmtPathNotMap, path[0:i])
		}

		if prev == nil {
			c.paths = currentValue
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
func (c *Config) getMap(path []string) (map[any]any, error) {
	currentValue := c.paths
	for i := 0; i < len(path)+1; i++ {
		if m, ok := currentValue.(map[any]any); !ok {
			return nil, fmt.Errorf(fmtPathNotMap, path[0:i])
		} else if i < len(path) {
			if _, ok := m[path[i]]; !ok {
				return nil, fmt.Errorf(fmtPathNotExist, path[0:i+1])
			} else {
				currentValue = m[path[i]]
			}
		}
	}

	return currentValue.(map[any]any), nil
}

// Set sets a value to a configuration path.
func (c *Config) Set(path []string, value any) error {
	if path == nil || len(path) == 0 {
		c.paths = value
		return nil
	}

	last := len(path) - 1
	if m, err := c.createMaps(path[0:last]); err != nil {
		return err
	} else {
		m[path[last]] = value
	}

	return nil
}

// Get returns a value from a configuration path.
func (c *Config) Get(path []string) (any, error) {
	if path == nil || len(path) == 0 {
		if c.paths == nil {
			return nil, fmt.Errorf(fmtPathNotExist, path)
		}
		return c.paths, nil
	}

	last := len(path) - 1
	if m, err := c.getMap(path[0:last]); err != nil {
		return nil, err
	} else {
		ret := m[path[last]]
		if ret == nil {
			return nil, fmt.Errorf(fmtPathNotExist, path)
		}
		return ret, nil
	}
}

// Elems returns a list of an elements for a path.
func (c *Config) Elems(path []string) ([]string, error) {
	var target map[any]any
	if path == nil || len(path) == 0 {
		if c.paths == nil {
			return nil, fmt.Errorf(fmtPathNotExist, path)
		}

		if m, ok := c.paths.(map[any]any); !ok {
			return nil, fmt.Errorf(fmtPathNotMap, path)
		} else {
			target = m
		}
	} else if m, err := c.getMap(path); err != nil {
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

// ForEach iterates over each value in the configuration.
func (c *Config) ForEach(path []string, fun func(path []string, value any)) {
	var target any
	if path == nil || len(path) == 0 {
		if c.paths == nil {
			return
		} else {
			target = c.paths
		}
	} else {
		last := len(path) - 1
		if m, err := c.getMap(path[0:last]); err != nil {
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
func (c *Config) Merge(low *Config) {
	low.ForEach(nil, func(path []string, value any) {
		if _, err := c.Get(path); err != nil {
			c.Set(path, value)
		}
	})
}

// UnmarshalYAML helps to unmarshal the configuration from a YAML document.
func (c *Config) UnmarshalYAML(unmarshal func(any) error) error {
	if err := unmarshal(&c.paths); err != nil {
		return fmt.Errorf("failed to unmarshal Config: %w", err)
	}
	return nil
}

// String returns a string representation of the configuration. Actually it is
// a valid YAML.
func (c *Config) String() string {
	if c.paths == nil {
		return ""
	}

	decoded, err := yaml.Marshal(c.paths)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal a config: %s", err))
	}

	return string(decoded)
}
