package output

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
)

var (
	// ErrKeyCollision reports a map in which two distinct keys have the same
	// string form, such as the integer 1 and the string "1".
	ErrKeyCollision = errors.New("map keys collide")
	// ErrNotNormalizable reports a value Normalize cannot make encodable:
	// a map key with no string form, a struct field whose declared type
	// cannot hold the normalised value, or a reference cycle.
	ErrNotNormalizable = errors.New("value cannot be normalised")
)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	stringerType      = reflect.TypeFor[fmt.Stringer]()
	anyType           = reflect.TypeFor[any]()
)

// Normalize returns value in a form every machine encoder can take: the
// shape of a document decoded from MessagePack - a Tarantool tuple, a Lua
// table - with no map keyed by an interface, a bool or a float. The JSON
// encoder applies it to every result and stream item; a core encoder for
// another format calls it when that format needs it.
//
// The rules:
//
//   - A map whose key type is an interface, a bool or a float becomes a
//     map[string]any. A string key stays as it is, a TextMarshaler key
//     becomes its text, an integer or a bool its decimal or true/false
//     spelling, a finite float its shortest 'g' formatting and a
//     non-finite one "NaN", "Infinity" or "-Infinity". A key of any other
//     kind, nil included, is ErrNotNormalizable. Maps keyed by strings,
//     integers or TextMarshalers keep their key type, as encoding/json
//     handles them.
//   - Two keys of one map with the same string form - 1 and "1", NaN and
//     "NaN", two NaNs - are ErrKeyCollision naming both; neither is
//     dropped.
//   - A float32 or float64 value that is NaN, +Inf or -Inf becomes the
//     string "NaN", "Infinity" or "-Infinity"; a finite float stays a
//     number. JSON has no number for them, and encoding/json refuses them.
//     The JSON type of such a value then depends on the value - a number
//     normally, a string when it is non-finite - which is how
//     encoding/json/v2 encodes a float under `json:",format:nonfinite"`
//     and how the proto3 JSON mapping encodes a double. A struct field
//     declared as a float cannot hold the string, so a non-finite value in
//     one is ErrNotNormalizable.
//   - []byte is left as it is, so JSON carries it as base64. MessagePack
//     tells a binary value from a string, and a JSON string keeps that
//     distinction only if it does not depend on the content: a binary value
//     that happens to be valid UTF-8 is still binary.
//   - A value whose type implements json.Marshaler or
//     encoding.TextMarshaler is left to its own marshalling.
//   - A struct with no exported and no embedded fields that implements
//     fmt.Stringer becomes its String form. encoding/json would render it
//     as {}, losing the value; go-tarantool's datetime.Datetime is such a
//     type.
//   - Slices, arrays, maps, pointers, interfaces and the exported fields of
//     structs, exported embedded ones included, are walked. A container
//     whose element type cannot hold a normalised element becomes a []any
//     or a map with any values; a struct field that cannot hold its
//     normalised value is ErrNotNormalizable - declare such a field as any.
//     Fields tagged json:"-" and the fields of an unexported embedded
//     struct are not walked. A reference cycle is ErrNotNormalizable.
//
// Normalize never mutates value: whatever it changes is a copy, and what
// it does not change is shared. A value that needs no change is returned
// as it is.
func Normalize(value any) (any, error) {
	walker := normalizer{visiting: map[visit]struct{}{}}

	normalized, changed, err := walker.walk(reflect.ValueOf(value))
	if err != nil {
		return nil, err
	}

	if !changed {
		return value, nil
	}

	return normalized.Interface(), nil
}

// visit identifies a pointer, map or slice on the path being walked.
type visit struct {
	ptr    uintptr
	typ    reflect.Type
	length int
}

// normalizer walks one value; visiting holds the references on the current
// path, so a cycle is found while a shared reference is not mistaken for one.
type normalizer struct {
	visiting map[visit]struct{}
}

// walk returns the normalised form of value and whether it differs from
// value. When it does not, the returned value is value itself.
func (n normalizer) walk(value reflect.Value) (reflect.Value, bool, error) {
	switch {
	case !value.IsValid():
		return value, false, nil
	case selfMarshaling(value):
		return value, false, nil
	case opaqueStringer(value):
		return stringForm(value)
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return value, false, nil
		}

		elem, changed, err := n.walk(value.Elem())
		if err != nil || !changed {
			return value, false, err
		}

		return elem, true, nil
	case reflect.Pointer:
		return n.walkPointer(value)
	case reflect.Map:
		return n.walkMap(value)
	case reflect.Slice:
		if value.IsNil() || value.Type().Elem().Kind() == reflect.Uint8 {
			return value, false, nil
		}

		return n.walkSequence(value)
	case reflect.Array:
		return n.walkSequence(value)
	case reflect.Struct:
		return n.walkStruct(value)
	case reflect.Float32, reflect.Float64:
		if name, ok := nonFiniteName(value.Float()); ok {
			return reflect.ValueOf(name), true, nil
		}

		return value, false, nil
	default:
		return value, false, nil
	}
}

// nonFiniteName returns the string that stands for a NaN, +Inf or -Inf
// float - the spelling encoding/json/v2 writes under format:nonfinite -
// and false for a finite one.
func nonFiniteName(f float64) (string, bool) {
	switch {
	case math.IsNaN(f):
		return "NaN", true
	case math.IsInf(f, 1):
		return "Infinity", true
	case math.IsInf(f, -1):
		return "-Infinity", true
	default:
		return "", false
	}
}

// enter marks a reference as being on the path; the returned function
// removes it. A reference already on the path is a cycle.
func (n normalizer) enter(value reflect.Value) (func(), error) {
	key := visit{ptr: value.Pointer(), typ: value.Type(), length: 0}
	if value.Kind() == reflect.Slice {
		key.length = value.Len()
	}

	if _, ok := n.visiting[key]; ok {
		return nil, fmt.Errorf("%w: a reference cycle through %s", ErrNotNormalizable, key.typ)
	}

	n.visiting[key] = struct{}{}

	return func() { delete(n.visiting, key) }, nil
}

func (n normalizer) walkPointer(value reflect.Value) (reflect.Value, bool, error) {
	if value.IsNil() {
		return value, false, nil
	}

	leave, err := n.enter(value)
	if err != nil {
		return reflect.Value{}, false, err
	}
	defer leave()

	elem, changed, err := n.walk(value.Elem())
	if err != nil || !changed {
		return value, false, err
	}

	if elem.Type() != value.Type().Elem() {
		return elem, true, nil
	}

	copied := reflect.New(elem.Type())
	copied.Elem().Set(elem)

	return copied, true, nil
}

// entry is one map entry with its key's string form.
type entry struct {
	name  string
	key   reflect.Value
	value reflect.Value
}

func (n normalizer) walkMap(value reflect.Value) (reflect.Value, bool, error) {
	stringKeys := needsStringKeys(value.Type().Key())

	if value.IsNil() {
		// encoding/json refuses the map type even when the map is nil.
		if stringKeys {
			return reflect.Zero(reflect.TypeFor[map[string]any]()), true, nil
		}

		return value, false, nil
	}

	leave, err := n.enter(value)
	if err != nil {
		return reflect.Value{}, false, err
	}
	defer leave()

	changed := stringKeys
	fits := true
	entries := make([]entry, 0, value.Len())

	iter := value.MapRange()
	for iter.Next() {
		elem, elemChanged, err := n.walk(iter.Value())
		if err != nil {
			return reflect.Value{}, false, err
		}

		changed = changed || elemChanged
		fits = fits && (!elemChanged || elem.Type().AssignableTo(value.Type().Elem()))
		entries = append(entries, entry{name: "", key: iter.Key(), value: elem})
	}

	if !changed {
		return value, false, nil
	}

	if !stringKeys {
		return rebuildMap(value.Type(), fits, entries), true, nil
	}

	return stringKeyedMap(entries)
}

// rebuildMap makes a map with the key type of mapType holding entries: of
// mapType itself when every value fits, with any values otherwise.
func rebuildMap(mapType reflect.Type, fits bool, entries []entry) reflect.Value {
	if !fits {
		mapType = reflect.MapOf(mapType.Key(), anyType)
	}

	rebuilt := reflect.MakeMapWithSize(mapType, len(entries))
	for _, item := range entries {
		rebuilt.SetMapIndex(item.key, item.value)
	}

	return rebuilt
}

// stringKeyedMap makes a map[string]any of entries, refusing two keys with
// the same string form. Entries are ordered by that form, so the collision
// reported is the same on every run.
func stringKeyedMap(entries []entry) (reflect.Value, bool, error) {
	for i := range entries {
		name, err := keyName(entries[i].key)
		if err != nil {
			return reflect.Value{}, false, err
		}

		entries[i].name = name
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].name != entries[j].name {
			return entries[i].name < entries[j].name
		}

		return describeKey(entries[i].key) < describeKey(entries[j].key)
	})

	rebuilt := make(map[string]any, len(entries))

	for i, item := range entries {
		if i > 0 && entries[i-1].name == item.name {
			return reflect.Value{}, false, fmt.Errorf("%w: %s and %s are both %q", ErrKeyCollision,
				describeKey(entries[i-1].key), describeKey(item.key), item.name)
		}

		rebuilt[item.name] = interfaceOf(item.value)
	}

	return reflect.ValueOf(rebuilt), true, nil
}

func (n normalizer) walkSequence(value reflect.Value) (reflect.Value, bool, error) {
	if value.Kind() == reflect.Slice {
		leave, err := n.enter(value)
		if err != nil {
			return reflect.Value{}, false, err
		}
		defer leave()
	}

	elemType := value.Type().Elem()
	elems := make([]reflect.Value, value.Len())
	changed := false
	fits := true

	for i := range elems {
		elem, elemChanged, err := n.walk(value.Index(i))
		if err != nil {
			return reflect.Value{}, false, err
		}

		elems[i] = elem
		changed = changed || elemChanged
		fits = fits && (!elemChanged || elem.Type().AssignableTo(elemType))
	}

	if !changed {
		return value, false, nil
	}

	var rebuilt reflect.Value

	switch {
	case !fits:
		rebuilt = reflect.MakeSlice(reflect.SliceOf(anyType), len(elems), len(elems))
	case value.Kind() == reflect.Slice:
		rebuilt = reflect.MakeSlice(value.Type(), len(elems), len(elems))
	default:
		rebuilt = reflect.New(value.Type()).Elem()
	}

	for i, elem := range elems {
		rebuilt.Index(i).Set(elem)
	}

	return rebuilt, true, nil
}

func (n normalizer) walkStruct(value reflect.Value) (reflect.Value, bool, error) {
	var copied reflect.Value

	for i := range value.NumField() {
		field := value.Type().Field(i)
		if !field.IsExported() || field.Tag.Get("json") == "-" {
			continue
		}

		normalized, changed, err := n.walk(value.Field(i))
		if err != nil {
			return reflect.Value{}, false, fmt.Errorf("field %s: %w", field.Name, err)
		}

		if !changed {
			continue
		}

		if !normalized.Type().AssignableTo(field.Type) {
			return reflect.Value{}, false, fmt.Errorf(
				"%w: field %s of type %s cannot hold a %s; declare it as any",
				ErrNotNormalizable, field.Name, field.Type, normalized.Type(),
			)
		}

		if !copied.IsValid() {
			copied = reflect.New(value.Type()).Elem()
			copied.Set(value)
		}

		copied.Field(i).Set(normalized)
	}

	if !copied.IsValid() {
		return value, false, nil
	}

	return copied, true, nil
}

// implements reports whether value's type implements iface, or its pointer
// type does and value is addressable, so the pointer method is reachable.
func implements(value reflect.Value, iface reflect.Type) bool {
	if value.Type().Implements(iface) {
		return true
	}

	return value.CanAddr() && reflect.PointerTo(value.Type()).Implements(iface)
}

// selfMarshaling reports whether value marshals itself, as JSON or as text.
// An interface value is judged by what it holds.
func selfMarshaling(value reflect.Value) bool {
	if value.Kind() == reflect.Interface {
		return false
	}

	return implements(value, jsonMarshalerType) || implements(value, textMarshalerType)
}

// opaqueStringer reports whether value is a struct with no exported and no
// embedded fields that implements fmt.Stringer.
func opaqueStringer(value reflect.Value) bool {
	if value.Kind() != reflect.Struct || !implements(value, stringerType) {
		return false
	}

	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.IsExported() || field.Anonymous {
			return false
		}
	}

	return true
}

// stringForm returns value's String form.
func stringForm(value reflect.Value) (reflect.Value, bool, error) {
	stringer, ok := value.Interface().(fmt.Stringer)
	if !ok {
		stringer, _ = value.Addr().Interface().(fmt.Stringer)
	}

	return reflect.ValueOf(stringer.String()), true, nil
}

// needsStringKeys reports whether a map keyed by keyType must be rebuilt
// with string keys to be encoded: encoding/json takes string, integer and
// TextMarshaler keys and nothing else.
func needsStringKeys(keyType reflect.Type) bool {
	if keyType.Implements(textMarshalerType) {
		return false
	}

	switch keyType.Kind() {
	case reflect.Interface, reflect.Bool, reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// keyName returns the string form of a map key.
func keyName(key reflect.Value) (string, error) {
	if key.Kind() == reflect.Interface {
		if key.IsNil() {
			return "", fmt.Errorf("%w: a nil map key", ErrNotNormalizable)
		}

		key = key.Elem()
	}

	if key.Kind() == reflect.String {
		return key.String(), nil
	}

	if marshaler, ok := key.Interface().(encoding.TextMarshaler); ok {
		text, err := marshaler.MarshalText()
		if err != nil {
			return "", fmt.Errorf("map key %s: %w", describeKey(key), err)
		}

		return string(text), nil
	}

	switch key.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(key.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr:
		return strconv.FormatUint(key.Uint(), 10), nil
	case reflect.Bool:
		return strconv.FormatBool(key.Bool()), nil
	case reflect.Float32, reflect.Float64:
		if name, ok := nonFiniteName(key.Float()); ok {
			return name, nil
		}

		return strconv.FormatFloat(key.Float(), 'g', -1, key.Type().Bits()), nil
	default:
		return "", fmt.Errorf("%w: a map key of type %s has no string form",
			ErrNotNormalizable, key.Type())
	}
}

// describeKey spells a key with its type, for an error message: a string
// quoted, anything else as fmt prints it.
func describeKey(key reflect.Value) string {
	if key.Kind() == reflect.Interface && !key.IsNil() {
		key = key.Elem()
	}

	if key.Kind() == reflect.String {
		return fmt.Sprintf("%q (%s)", key.String(), key.Type())
	}

	return fmt.Sprintf("%v (%s)", key.Interface(), key.Type())
}

// interfaceOf returns what value holds, nil for the zero Value.
func interfaceOf(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}

	return value.Interface()
}
