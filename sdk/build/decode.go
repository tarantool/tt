package build

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// This file decodes the fields of a build block into the configuration of a
// backend. The block comes as the generic tree a TOML decoder makes when it
// has no struct to fill: tables are map[string]any, arrays []any, strings,
// int64, float64 and bool, and date-times of their own types. The decoder is
// the core's and buildtest's alike, so that a backend's tests see the errors
// a manifest gets.

var (
	// ErrConfig reports a Config that does not return what [Backend.Config]
	// says it returns.
	ErrConfig = errors.New("invalid configuration of a backend")
	// errNoConfig reports a backend without Config.
	errNoConfig = errors.New("no Config")
)

// Fields returns the keys of the build block the configuration of b has, in
// the order of their fields, those of an embedded struct in its place. An
// error wraps [ErrConfig] and says what of the configuration the decoder
// cannot fill.
func (b Backend) Fields() ([]string, error) {
	cfg, err := b.config()
	if err != nil {
		return nil, err
	}

	fields, err := structFields(cfg.Elem().Type())
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		keys = append(keys, field.key)
	}

	return keys, nil
}

// Decode decodes rest, the fields of a build block but the common ones, into
// a fresh configuration of b, which it returns. The keys of rest the
// configuration has no field for, at any depth, are returned with their
// paths, sorted, rather than failing the decode: the core tells a field of
// another backend from an unknown one, and an unknown one of a manifest of a
// newer format from a mistake. A value of a type its field cannot hold is a
// [*FieldError] with the path of the value ("cflags", "sources[1]",
// "platforms.linux.cflags"). An error of the configuration itself wraps
// [ErrConfig].
func (b Backend) Decode(rest map[string]any) (any, []string, error) {
	cfg, err := b.config()
	if err != nil {
		return nil, nil, err
	}

	// The configuration is checked whole before a value goes in, so that
	// one the decoder cannot fill fails whatever the block holds.
	_, err = structFields(cfg.Elem().Type())
	if err != nil {
		return nil, nil, err
	}

	var unknown []string

	err = decodeStruct(cfg.Elem(), rest, "", &unknown)
	if err != nil {
		return nil, nil, err
	}

	slices.Sort(unknown)

	return cfg.Interface(), unknown, nil
}

// config calls b.Config and checks that it returned a pointer to a struct.
func (b Backend) config() (reflect.Value, error) {
	if b.Config == nil {
		return reflect.Value{}, fmt.Errorf("%w: %w", ErrConfig, errNoConfig)
	}

	cfg := reflect.ValueOf(b.Config())
	if cfg.Kind() != reflect.Pointer || cfg.IsNil() || cfg.Elem().Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("%w: Config returned %s, not a pointer to a struct",
			ErrConfig, describeGo(cfg))
	}

	return cfg, nil
}

// describeGo names the Go type of v for an error of a configuration.
func describeGo(value reflect.Value) string {
	if !value.IsValid() {
		return "nil"
	}

	if value.Kind() == reflect.Pointer && value.IsNil() {
		return "a nil " + value.Type().String()
	}

	return value.Type().String()
}

// configField is a field of a configuration struct: its key in the block and
// the index path of the field, through the embedded structs it is in.
type configField struct {
	key   string
	index []int
}

// structFields lists the fields of the struct type typ by their keys,
// checking that the decoder can fill each one, at any depth, and that no key
// of a struct is taken twice.
func structFields(typ reflect.Type) ([]configField, error) {
	return checkedFields(typ, map[reflect.Type]bool{})
}

// checkedFields is structFields of typ; visiting holds the struct types being
// checked, so that a recursive one is refused rather than walked for ever.
func checkedFields(typ reflect.Type, visiting map[reflect.Type]bool) ([]configField, error) {
	if visiting[typ] {
		return nil, fmt.Errorf("%w: %s is %w", ErrConfig, typ, errRecursiveType)
	}

	visiting[typ] = true
	defer delete(visiting, typ)

	var fields []configField

	err := collectFields(typ, nil, &fields, visiting)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}

	for _, field := range fields {
		if seen[field.key] {
			return nil, fmt.Errorf("%w: %s has two fields of key %q", ErrConfig, typ, field.key)
		}

		seen[field.key] = true
	}

	return fields, nil
}

// collectFields appends the fields of the struct type typ to fields, those
// of an embedded struct flattened in their place; index is the index path of
// typ.
func collectFields(
	typ reflect.Type, index []int, fields *[]configField, visiting map[reflect.Type]bool,
) error {
	for number := range typ.NumField() {
		field := typ.Field(number)

		// An embedded struct holds fields of the block even when its type
		// is unexported, as it does for encoding/json.
		embedded := field.Anonymous && field.Type.Kind() == reflect.Struct &&
			field.Tag.Get("toml") == ""
		if !field.IsExported() && !embedded {
			continue
		}

		key, skip := fieldKey(field)
		if skip {
			continue
		}

		path := append(slices.Clone(index), number)

		if embedded {
			err := collectFields(field.Type, path, fields, visiting)
			if err != nil {
				return err
			}

			continue
		}

		err := checkType(field.Type, visiting)
		if err != nil {
			return fmt.Errorf("field %s of %s: %w", field.Name, typ, err)
		}

		*fields = append(*fields, configField{key: key, index: path})
	}

	return nil
}

// fieldKey returns the key of a struct field: the name of its toml tag, or
// the name of the field when the tag gives none; skip is set for a field the
// tag leaves out ("-").
func fieldKey(field reflect.StructField) (string, bool) {
	name, _, _ := strings.Cut(field.Tag.Get("toml"), ",")

	switch name {
	case "-":
		return "", true
	case "":
		return field.Name, false
	default:
		return name, false
	}
}

var (
	// errUnsupportedType reports a type of a field the decoder cannot fill.
	errUnsupportedType = errors.New("the decoder cannot fill")
	// errRecursiveType reports a struct type that holds itself.
	errRecursiveType = errors.New("a struct that holds itself")
)

// checkType checks that the decoder can fill a value of type typ.
func checkType(typ reflect.Type, visiting map[reflect.Type]bool) error {
	switch typ.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return nil
	case reflect.Interface:
		if typ.NumMethod() != 0 {
			return fmt.Errorf("%w: %w %s", ErrConfig, errUnsupportedType, typ)
		}

		return nil
	case reflect.Pointer, reflect.Slice:
		return checkType(typ.Elem(), visiting)
	case reflect.Map:
		if typ.Key().Kind() != reflect.String {
			return fmt.Errorf("%w: %w %s: the keys of a table are strings",
				ErrConfig, errUnsupportedType, typ)
		}

		return checkType(typ.Elem(), visiting)
	case reflect.Struct:
		_, err := checkedFields(typ, visiting)

		return err
	default:
		return fmt.Errorf("%w: %w %s", ErrConfig, errUnsupportedType, typ)
	}
}

// decodeStruct decodes table into the struct dst; path is the path of the
// table from the block, "" for the block itself. The keys dst has no field
// for are appended to unknown.
func decodeStruct(dst reflect.Value, table map[string]any, path string, unknown *[]string) error {
	fields, err := structFields(dst.Type())
	if err != nil {
		return err
	}

	byKey := make(map[string]configField, len(fields))
	for _, field := range fields {
		byKey[field.key] = field
	}

	for _, key := range sortedKeys(table) {
		child := joinKey(path, key)

		field, ok := byKey[key]
		if !ok {
			*unknown = append(*unknown, child)

			continue
		}

		err := decodeValue(dst.FieldByIndex(field.index), table[key], child, unknown)
		if err != nil {
			return err
		}
	}

	return nil
}

// decodeValue decodes value into dst; path is the path of the value from the
// block.
func decodeValue(dst reflect.Value, value any, path string, unknown *[]string) error {
	switch dst.Kind() {
	case reflect.Interface:
		dst.Set(reflect.ValueOf(&value).Elem())
	case reflect.Pointer:
		elem := reflect.New(dst.Type().Elem())

		err := decodeValue(elem.Elem(), value, path, unknown)
		if err != nil {
			return err
		}

		dst.Set(elem)
	case reflect.String:
		text, ok := value.(string)
		if !ok {
			return typeError(path, "a string", value)
		}

		dst.SetString(text)
	case reflect.Bool:
		flag, ok := value.(bool)
		if !ok {
			return typeError(path, "a boolean", value)
		}

		dst.SetBool(flag)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return decodeInt(dst, value, path)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return decodeUint(dst, value, path)
	case reflect.Float32, reflect.Float64:
		return decodeFloat(dst, value, path)
	case reflect.Slice:
		return decodeSlice(dst, value, path, unknown)
	case reflect.Map:
		return decodeMap(dst, value, path, unknown)
	case reflect.Struct:
		table, ok := value.(map[string]any)
		if !ok {
			return typeError(path, "a table", value)
		}

		return decodeStruct(dst, table, path, unknown)
	default:
		// structFields refused the type before any value went in.
		return fmt.Errorf("%w: %s: %w %s", ErrConfig, path, errUnsupportedType, dst.Type())
	}

	return nil
}

// decodeInt decodes an integer into dst, an integer of some size.
func decodeInt(dst reflect.Value, value any, path string) error {
	number, ok := value.(int64)
	if !ok {
		return typeError(path, "an integer", value)
	}

	if dst.OverflowInt(number) {
		return FieldErrorf(path, "%d is out of the range of the field", number)
	}

	dst.SetInt(number)

	return nil
}

// decodeUint decodes a non-negative integer into dst, an unsigned integer
// of some size.
func decodeUint(dst reflect.Value, value any, path string) error {
	number, ok := value.(int64)
	if !ok {
		return typeError(path, "an integer", value)
	}

	if number < 0 || dst.OverflowUint(uint64(number)) {
		return FieldErrorf(path, "%d is out of the range of the field", number)
	}

	dst.SetUint(uint64(number))

	return nil
}

// decodeFloat decodes a number into dst, a float of some size: an integer
// is a number too.
func decodeFloat(dst reflect.Value, value any, path string) error {
	var number float64

	switch value := value.(type) {
	case float64:
		number = value
	case int64:
		number = float64(value)
	default:
		return typeError(path, "a number", value)
	}

	if dst.Kind() == reflect.Float32 && !math.IsInf(number, 0) && !math.IsNaN(number) &&
		dst.OverflowFloat(number) {
		return FieldErrorf(path, "%v is out of the range of the field", number)
	}

	dst.SetFloat(number)

	return nil
}

// decodeSlice decodes an array into dst, a slice.
func decodeSlice(dst reflect.Value, value any, path string, unknown *[]string) error {
	array, ok := value.([]any)
	if !ok {
		return typeError(path, "an array", value)
	}

	slice := reflect.MakeSlice(dst.Type(), len(array), len(array))

	for index, element := range array {
		err := decodeValue(slice.Index(index), element, path+"["+strconv.Itoa(index)+"]",
			unknown)
		if err != nil {
			return err
		}
	}

	dst.Set(slice)

	return nil
}

// decodeMap decodes a table into dst, a map with string keys.
func decodeMap(dst reflect.Value, value any, path string, unknown *[]string) error {
	table, ok := value.(map[string]any)
	if !ok {
		return typeError(path, "a table", value)
	}

	out := reflect.MakeMapWithSize(dst.Type(), len(table))

	for _, key := range sortedKeys(table) {
		elem := reflect.New(dst.Type().Elem()).Elem()

		err := decodeValue(elem, table[key], joinKey(path, key), unknown)
		if err != nil {
			return err
		}

		out.SetMapIndex(reflect.ValueOf(key).Convert(dst.Type().Key()), elem)
	}

	dst.Set(out)

	return nil
}

// typeError reports a value that is not of the TOML type want.
func typeError(path, want string, value any) error {
	return FieldErrorf(path, "must be %s, not %s", want, describeTOML(value))
}

// describeTOML names the TOML type of a value of the generic tree.
func describeTOML(value any) string {
	switch value.(type) {
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case int64:
		return "an integer"
	case float64:
		return "a float"
	case []any:
		return "an array"
	case map[string]any:
		return "a table"
	default:
		// The date-times, whose types are the TOML decoder's own.
		return "a date or a time"
	}
}

// joinKey is the path of a key of the table at path.
func joinKey(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

// sortedKeys returns the keys of table in byte order.
func sortedKeys(table map[string]any) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
