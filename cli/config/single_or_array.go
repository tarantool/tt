package config

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// SingleOrArray is a helper type for flexible customization of fields that can contain either
// a single value or a list of values, of the same original data type.
//
// Solution based on: https://gist.github.com/SVilgelm/0854d06308e36228857d08571d20aaf1
type SingleOrArray[T any] []T

// NewSingleOrArray creates SingleOrArray object.
func NewSingleOrArray[T any](v ...T) SingleOrArray[T] {
	return append([]T{}, v...)
}

// UnmarshalJSON implements json.Unmarshaler interface.
func (o *SingleOrArray[T]) UnmarshalJSON(data []byte) error {
	var ret []T

	if json.Unmarshal(data, &ret) != nil {
		var single T

		err := json.Unmarshal(data, &single)
		if err != nil {
			// The decoder adds the field context to its own error types only.
			return err //nolint:wrapcheck // Unmarshaler must return the codec's error as is.
		}

		ret = []T{single}
	}

	*o = ret

	return nil
}

// MarshalJSON implements json.Marshaler interface.
func (o SingleOrArray[T]) MarshalJSON() ([]byte, error) {
	if len(o) == 1 {
		return json.Marshal(o[0]) //nolint:wrapcheck // The encoder adds the type context itself.
	}

	return json.Marshal([]T(o)) //nolint:wrapcheck // The encoder adds the type context itself.
}

// UnmarshalYAML implements yaml.Unmarshaler interface.
func (o *SingleOrArray[T]) UnmarshalYAML(node *yaml.Node) error {
	var ret []T

	if node.Decode(&ret) != nil {
		var single T

		err := node.Decode(&single)
		if err != nil {
			// The decoder merges *yaml.TypeError values and keeps decoding.
			return err //nolint:wrapcheck // Unmarshaler must return the codec's error as is.
		}

		ret = []T{single}
	}

	*o = ret

	return nil
}

// MarshalYAML implements yaml.Marshaler interface.
func (o SingleOrArray[T]) MarshalYAML() (any, error) {
	var value any

	value = []T(o)

	if len(o) == 1 {
		value = o[0]
	}

	return value, nil
}

// FieldStringArrayType is alias for the custom type used `SingleOrArray` with strings
// to handle as a single string as well as a list of strings.
type FieldStringArrayType = SingleOrArray[string]
