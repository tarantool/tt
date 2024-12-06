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
		var s T
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		ret = []T{s}
	}
	*o = ret
	return nil
}

// MarshalJSON implements json.Marshaler interface.
func (o SingleOrArray[T]) MarshalJSON() ([]byte, error) {
	if len(o) == 1 {
		return json.Marshal(o[0])
	}
	return json.Marshal([]T(o))
}

// UnmarshalYAML implements yaml.Unmarshaler interface.
func (o *SingleOrArray[T]) UnmarshalYAML(node *yaml.Node) error {
	var ret []T
	if node.Decode(&ret) != nil {
		var s T
		if err := node.Decode(&s); err != nil {
			return err
		}
		ret = []T{s}
	}
	*o = ret
	return nil
}

// MarshalYAML implements yaml.Marshaler interface.
func (o SingleOrArray[T]) MarshalYAML() (any, error) {
	var v any
	v = []T(o)
	if len(o) == 1 {
		v = o[0]
	}
	return v, nil
}

// FieldStringArrayType is alias for the custom type used `SingleOrArray` with strings
// to handle as a single string as well as a list of strings.
type FieldStringArrayType = SingleOrArray[string]
