package cluster

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	fmtUnexpected  = "unexpected type %v, expected %s"
	fmtParseFailed = "failed to parse %s"
)

// ValidateError describes a schema validation error.
type ValidateError struct {
	path []string
	errs []error
}

// Path returns a schema path to a configuration value.
func (err ValidateError) Path() []string {
	return err.path
}

// Error returns a string representation of the validation error.
func (err ValidateError) Error() string {
	if len(err.errs) < 1 {
		return fmt.Sprintf("failed to validate %q", err.path)
	}

	return fmt.Sprintf("failed to validate %q: %s",
		err.path, errors.Join(err.errs...))
}

// Error returns a list of errors for the corresponding path.
func (err ValidateError) Unwrap() []error {
	return err.errs
}

// wrapValidateErrors wraps errors with the validation path:
// * for a ValidateError object it adds the path into the error path.
// * for another types of errors create a ValidateError analogs.
func wrapValidateErrors(path []string, errs ...error) error {
	var (
		rawErrs      []error
		validateErrs []ValidateError
	)

	// Make errors flat and convert it into a slice of ValidateError objects.
	for _, err := range errs {
		if err == nil {
			continue
		}
		if unwrapErr, ok := err.(interface{ Unwrap() []error }); ok {
			for _, err := range unwrapErr.Unwrap() {
				if validateErr, ok := err.(ValidateError); ok {
					validateErr.path = append(path, validateErr.path...)
					validateErrs = append(validateErrs, validateErr)
				} else {
					rawErrs = append(rawErrs, err)
				}
			}
		} else {
			rawErrs = append(rawErrs, err)
		}
	}

	// Create an empty error if nothing exist.
	if len(validateErrs) == 0 || len(rawErrs) > 0 {
		validateErrs = append(validateErrs, ValidateError{
			path: path,
			errs: rawErrs,
		})
	}

	return errors.Join(mergeValidateErrors(validateErrs)...)
}

// mergeValidateErrors merges ValidateError objects with the same path into
// a one error.
func mergeValidateErrors(errs []ValidateError) []error {
	// Sort errors by path.
	sort.Slice(errs, func(i, j int) bool {
		left := errs[i].Path()
		right := errs[j].Path()

		if len(left) != len(right) {
			return len(left) < len(right)
		}
		for i := 0; i < len(left); i++ {
			if left[i] != right[i] {
				return left[i] < right[i]
			}
		}
		return false
	})

	// Merge errors with a same path.
	merged := []error{}
	for _, err := range errs {
		if len(merged) > 0 {
			lastMerged := merged[len(merged)-1].(ValidateError)
			if reflect.DeepEqual(lastMerged.path, err.path) {
				lastMerged.errs = append(lastMerged.errs, err.errs...)
				merged[len(merged)-1] = lastMerged
				continue
			}
		}
		merged = append(merged, err)
	}

	return merged
}

// Validator validates a value.
type Validator interface {
	// Validate validates the value and returns a validated one.
	Validate(value any) (any, error)
}

// AnyValidator allows any values, but not nil.
type AnyValidator struct {
}

// Validate returns the value if it is not nil or an error.
func (validator AnyValidator) Validate(value any) (any, error) {
	if value == nil {
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf("a value expected, got nil"))
	}
	return value, nil
}

// StringValidator allows only string-compatible values.
type StringValidator struct {
}

// Validate returns a string value or an error.
func (validator StringValidator) Validate(value any) (any, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
		float32, float64, bool:
		return fmt.Sprint(v), nil
	default:
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf(fmtUnexpected, reflect.TypeOf(value), "string"))
	}
}

// BooleanValidator allows only boolean-compatible values.
type BooleanValidator struct {
}

// Validate returns a boolean value or an error.
func (validator BooleanValidator) Validate(value any) (any, error) {
	dst := value
	if str, ok := value.(string); ok {
		dst = strings.ToLower(str)
	}
	switch dst {
	case true:
		fallthrough
	case 1:
		fallthrough
	case "true":
		return true, nil
	case false:
		fallthrough
	case 0:
		fallthrough
	case "false":
		return false, nil
	default:
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf(fmtUnexpected, reflect.TypeOf(value), "boolean"))
	}
}

// IntegerValidator allows only integer-compatible values.
type IntegerValidator struct {
}

// Validate returns an integer value or an error.
func (validator IntegerValidator) Validate(value any) (any, error) {
	switch v := value.(type) {
	case string:
		ivalue, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
				fmt.Errorf(fmtParseFailed, "integer"))
		}
		return int(ivalue), nil
	case int, int8, int16, int32, int64:
		return int(reflect.ValueOf(v).Int()), nil
	case uint, uint8, uint16, uint32, uint64:
		return int(reflect.ValueOf(v).Uint()), nil
	default:
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf(fmtUnexpected, reflect.TypeOf(value), "integer"))
	}
}

// NumberValidator allows only number-compatible values.
type NumberValidator struct {
}

// Validate returns a number value or an error.
func (validator NumberValidator) Validate(value any) (any, error) {
	switch v := value.(type) {
	case string:
		fvalue, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
				fmt.Errorf(fmtParseFailed, "number"))
		}
		return fvalue, nil
	case int, int8, int16, int32, int64:
		return float64(reflect.ValueOf(v).Int()), nil
	case uint, uint8, uint16, uint32, uint64:
		return float64(reflect.ValueOf(v).Uint()), nil
	case float32, float64:
		return reflect.ValueOf(v).Float(), nil
	default:
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf(fmtUnexpected, reflect.TypeOf(value), "number"))
	}
}

// SequenceValidator allows to combine validators in a sequence.
type SequenceValidator struct {
	validators []Validator
}

// MakeSequenceValidator creates a new SequenceValidator object.
func MakeSequenceValidator(validators ...Validator) SequenceValidator {
	return SequenceValidator{
		validators: validators,
	}
}

// Validate returns a first validated value from the sequence or an error.
func (validator SequenceValidator) Validate(value any) (any, error) {
	var errs []error
	for _, v := range validator.validators {
		if ret, err := v.Validate(value); err == nil {
			return ret, nil
		} else {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return nil, wrapValidateErrors([]string{}, errs...)
	}
	return value, nil
}

// AllowedValidator allows a set of values.
type AllowedValidator struct {
	item    Validator
	allowed []any
}

// MakeAllowedValidator creates a new AllowedValidator object.
func MakeAllowedValidator(validator Validator, allowed []any) AllowedValidator {
	return AllowedValidator{
		item:    validator,
		allowed: allowed,
	}
}

// Validate returns a validated value or an error.
func (validator AllowedValidator) Validate(value any) (any, error) {
	validated, err := validator.item.Validate(value)
	if err != nil {
		return nil, err
	}

	val := reflect.ValueOf(validated)
	if !val.Comparable() {
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf("is not comparable"))
	}

	for _, allowed := range validator.allowed {
		allowedValue := reflect.ValueOf(allowed)
		if allowedValue.CanConvert(val.Type()) {
			if val.Equal(allowedValue.Convert(val.Type())) {
				return validated, nil
			}
		}
	}

	return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
		fmt.Errorf("should be one of %v", validator.allowed))
}

// ArrayValidator allows an array of values.
type ArrayValidator struct {
	item Validator
}

// MakeArrayValidator create a new ArrayValidator object with a validator for
// values.
func MakeArrayValidator(itemValidator Validator) ArrayValidator {
	return ArrayValidator{
		item: itemValidator,
	}
}

// Validate returns a validated array or an error.
func (validator ArrayValidator) Validate(value any) (any, error) {
	array, ok := value.([]any)
	if !ok {
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf("must be an array"))
	}

	var (
		out  []any
		errs []error
	)
	for _, val := range array {
		if ret, err := validator.item.Validate(val); err == nil {
			out = append(out, ret)
		} else {
			err = wrapValidateErrors([]string{}, err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return nil, wrapValidateErrors(nil, errs...)
	}
	return out, nil
}

// RecordValidator allows only a record values.
type RecordValidator struct {
	items map[string]Validator
}

// MakeRecordValidator create a new RecordValidator object with a specified
// record schema.
func MakeRecordValidator(items map[string]Validator) RecordValidator {
	return RecordValidator{
		items: items,
	}
}

// Validate returns a validated record or an error.
func (validator RecordValidator) Validate(value any) (any, error) {
	vmap, ok := value.(map[any]any)
	if !ok {
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf("must be a map"))
	}

	var (
		out  = make(map[any]any)
		errs []error
	)
	for k, validator := range validator.items {
		if val, ok := vmap[k]; ok {
			if ret, err := validator.Validate(val); err == nil {
				out[k] = ret
			} else {
				errs = append(errs, wrapValidateErrors([]string{k}, err))
			}
		}
	}

	if len(errs) > 0 {
		return nil, wrapValidateErrors(nil, errs...)
	}
	return out, nil
}

// MapValidator allows only a map.
type MapValidator struct {
	key   Validator
	value Validator
}

// MakeMapValidator create a new MapValidator object with specified a key and
// a value validator.
func MakeMapValidator(key Validator, value Validator) MapValidator {
	return MapValidator{
		key:   key,
		value: value,
	}
}

// Validate returns a validated map or an error.
func (validator MapValidator) Validate(value any) (any, error) {
	vmap, ok := value.(map[any]any)
	if !ok {
		return nil, wrapValidateErrors([]string{fmt.Sprint(value)},
			fmt.Errorf("must be a map"))
	}

	var (
		out  = make(map[any]any)
		errs []error
	)

	for key, value := range vmap {
		rkey, err := validator.key.Validate(key)
		if err != nil {
			err = wrapValidateErrors(nil, err)
			errs = append(errs, err)
			continue
		}
		rvalue, err := validator.value.Validate(value)
		if err != nil {
			path := []string{fmt.Sprint(key)}
			err = wrapValidateErrors(path, err)
			errs = append(errs, err)
			continue
		}
		out[rkey] = rvalue
	}

	if len(errs) > 0 {
		return nil, wrapValidateErrors(nil, errs...)
	}
	return out, nil
}
