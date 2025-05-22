package cluster_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/lib/cluster"
)

var (
	_ cluster.Validator = cluster.AnyValidator{}
	_ cluster.Validator = cluster.StringValidator{}
	_ cluster.Validator = cluster.BooleanValidator{}
	_ cluster.Validator = cluster.IntegerValidator{}
	_ cluster.Validator = cluster.NumberValidator{}
	_ cluster.Validator = cluster.SequenceValidator{}
	_ cluster.Validator = cluster.AllowedValidator{}
	_ cluster.Validator = cluster.ArrayValidator{}
	_ cluster.Validator = cluster.RecordValidator{}
	_ cluster.Validator = cluster.MapValidator{}
)

// validateFunc helps to use a function as the Validator interface.
type validateFunc func(value any) (any, error)

func (f validateFunc) Validate(value any) (any, error) {
	return f(value)
}

func TestAnyValidator_nil(t *testing.T) {
	value, err := cluster.AnyValidator{}.Validate(nil)

	assert.Nil(t, value)
	assert.EqualError(t, err,
		"invalid path \"\": a value expected, got nil")
}

func TestAnyValidator_value(t *testing.T) {
	cases := []any{
		"foo",
		1,
		1.3,
		true,
		struct{}{},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			value, err := cluster.AnyValidator{}.Validate(tc)
			assert.Equal(t, tc, value)
			assert.NoError(t, err)
		})
	}
}

func TestStringValidator_value(t *testing.T) {
	cases := []struct {
		Value    any
		Expected string
	}{
		{true, "true"},
		{int(1), "1"},
		{int8(2), "2"},
		{int16(3), "3"},
		{int32(4), "4"},
		{int64(5), "5"},
		{uint(6), "6"},
		{uint8(7), "7"},
		{uint16(8), "8"},
		{uint32(9), "9"},
		{uint64(10), "10"},
		{float32(1.1), "1.1"},
		{float64(2.1), "2.1"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(reflect.TypeOf(tc.Value)), func(t *testing.T) {
			value, err := cluster.StringValidator{}.Validate(tc.Value)
			assert.Equal(t, tc.Expected, value)
			assert.NoError(t, err)
		})
	}
}

func TestStringValidator_unsupported(t *testing.T) {
	cases := []struct {
		Value    any
		Expected string
	}{
		{
			nil,
			"invalid path \"\": " +
				"unexpected value \"<nil>\" of type <nil>, expected string",
		},
		{
			struct{}{},
			"invalid path \"\": " +
				"unexpected value \"{}\" of type struct {}, expected string",
		},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.Value), func(t *testing.T) {
			value, err := cluster.StringValidator{}.Validate(tc.Value)
			assert.Nil(t, value)
			assert.EqualError(t, err, tc.Expected)
		})
	}
}

func TestBooleanValidator_value(t *testing.T) {
	cases := []struct {
		Value    any
		Expected bool
	}{
		{false, false},
		{0, false},
		{"false", false},
		{"FaLsE", false},
		{true, true},
		{1, true},
		{"true", true},
		{"TrUe", true},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.Value), func(t *testing.T) {
			value, err := cluster.BooleanValidator{}.Validate(tc.Value)
			assert.Equal(t, tc.Expected, value)
			assert.NoError(t, err)
		})
	}
}

func TestBooleanValidator_unsupported(t *testing.T) {
	cases := []struct {
		Value    any
		Expected string
	}{
		{nil, "invalid path \"\": " +
			"unexpected value \"<nil>\" of type <nil>, expected boolean"},
		{"foo", "invalid path \"\": " +
			"unexpected value \"foo\" of type string, expected boolean"},
		{2, "invalid path \"\": " +
			"unexpected value \"2\" of type int, expected boolean"},
		{1.0, "invalid path \"\": " +
			"unexpected value \"1\" of type float64, expected boolean"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.Value), func(t *testing.T) {
			value, err := cluster.BooleanValidator{}.Validate(tc.Value)
			assert.Nil(t, value)
			assert.EqualError(t, err, tc.Expected)
		})
	}
}

func TestIntegerValidator_supported(t *testing.T) {
	cases := []struct {
		Value    any
		Expected int
	}{
		{"-13", -13},
		{"14", 14},
		{int(1), 1},
		{int8(2), 2},
		{int16(3), 3},
		{int32(4), 4},
		{int64(5), 5},
		{uint(6), 6},
		{uint8(7), 7},
		{uint16(8), 8},
		{uint32(9), 9},
		{uint64(10), 10},
	}

	for _, tc := range cases {
		v := tc.Value
		t.Run(fmt.Sprintf("%v_%s", v, reflect.TypeOf(v)), func(t *testing.T) {
			value, err := cluster.IntegerValidator{}.Validate(v)
			assert.Equal(t, tc.Expected, value)
			assert.NoError(t, err)
		})
	}
}

func TestIntegerValidator_unsupported(t *testing.T) {
	cases := []struct {
		Value    any
		Expected string
	}{
		{nil, "invalid path \"\": " +
			"unexpected value \"<nil>\" of type <nil>, expected integer"},
		{false, "invalid path \"\": " +
			"unexpected value \"false\" of type bool, expected integer"},
		{"foo", "invalid path \"\": failed to parse value \"foo\" to type integer"},
		{1.0, "invalid path \"\": " +
			"unexpected value \"1\" of type float64, expected integer"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.Value), func(t *testing.T) {
			value, err := cluster.IntegerValidator{}.Validate(tc.Value)
			assert.Nil(t, value)
			assert.EqualError(t, err, tc.Expected)
		})
	}
}

func TestNumberValidator_supported(t *testing.T) {
	cases := []struct {
		Value    any
		Expected float64
	}{
		{"-13", float64(-13)},
		{"14", float64(14)},
		{"-13.2", float64(-13.2)},
		{"14.3", float64(14.3)},
		{int(1), float64(1)},
		{int8(2), float64(2)},
		{int16(3), float64(3)},
		{int32(4), float64(4)},
		{int64(5), float64(5)},
		{uint(6), float64(6)},
		{uint8(7), float64(7)},
		{uint16(8), float64(8)},
		{uint32(9), float64(9)},
		{uint64(10), float64(10)},
		{float32(2), float64(2)},
		{float64(12.2), float64(12.2)},
	}

	for _, tc := range cases {
		v := tc.Value
		t.Run(fmt.Sprintf("%v_%s", v, reflect.TypeOf(v)), func(t *testing.T) {
			value, err := cluster.NumberValidator{}.Validate(v)
			assert.Equal(t, tc.Expected, value)
			assert.NoError(t, err)
		})
	}
}

func TestNumberValidator_unsupported(t *testing.T) {
	cases := []struct {
		Value    any
		Expected string
	}{
		{nil, "invalid path \"\": unexpected value \"<nil>\" of type <nil>, expected number"},
		{false, "invalid path \"\": unexpected value \"false\" of type bool, expected number"},
		{"foo", "invalid path \"\": failed to parse value \"foo\" to type number"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.Value), func(t *testing.T) {
			value, err := cluster.NumberValidator{}.Validate(tc.Value)
			assert.Nil(t, value)
			assert.EqualError(t, err, tc.Expected)
		})
	}
}

func TestSequenceValidator_single(t *testing.T) {
	var input any
	value, err := cluster.MakeSequenceValidator(
		validateFunc(func(value any) (any, error) {
			input = value
			return "foo", nil
		})).Validate(123)

	assert.Equal(t, 123, input)
	assert.Equal(t, "foo", value)
	assert.NoError(t, err)
}

func TestSequenceValidator_error(t *testing.T) {
	value, err := cluster.MakeSequenceValidator(
		validateFunc(func(any) (any, error) {
			return nil, fmt.Errorf("foo1")
		})).Validate(123)

	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"\": foo1")
}

func TestSequenceValidator_one_of(t *testing.T) {
	calls := 0
	value, err := cluster.MakeSequenceValidator(
		validateFunc(func(any) (any, error) {
			calls++
			return "foo", nil
		}),
		validateFunc(func(any) (any, error) {
			calls++
			return nil, fmt.Errorf("foo")
		})).Validate(123)

	assert.Equal(t, 1, calls)
	assert.Equal(t, "foo", value)
	assert.NoError(t, err)
}

func TestSequenceValidator_errors(t *testing.T) {
	var (
		calls = 0
		err1  = fmt.Errorf("foo1")
		err2  = fmt.Errorf("foo2")
	)
	value, err := cluster.MakeSequenceValidator(
		validateFunc(func(any) (any, error) {
			calls++
			return nil, err1
		}),
		validateFunc(func(any) (any, error) {
			calls++
			return nil, err2
		})).Validate(123)

	assert.Equal(t, 2, calls)
	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"\": foo1\nfoo2")
}

func TestSequenceValidator_empty(t *testing.T) {
	anyValue := 123
	value, err := cluster.MakeSequenceValidator().Validate(anyValue)

	assert.Equal(t, anyValue, value)
	assert.NoError(t, err)
}

func TestSequenctValidator_number_string(t *testing.T) {
	validator := cluster.MakeSequenceValidator(
		cluster.NumberValidator{},
		cluster.StringValidator{})

	cases := []struct {
		Value    any
		Expected any
		Error    string
	}{
		{nil, nil, "invalid path \"\": " +
			"unexpected value \"<nil>\" of type <nil>, expected number\n" +
			"unexpected value \"<nil>\" of type <nil>, expected string"},
		{struct{}{}, nil, "invalid path \"\": " +
			"unexpected value \"{}\" of type struct {}, expected number\n" +
			"unexpected value \"{}\" of type struct {}, expected string"},
		{9, float64(9), ""},
		{"9", float64(9), ""},
		{"1.1", float64(1.1), ""},
		{false, "false", ""},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.Value), func(t *testing.T) {
			value, err := validator.Validate(tc.Value)
			if tc.Error != "" {
				assert.Nil(t, value)
				assert.EqualError(t, err, tc.Error)
			} else {
				assert.Equal(t, tc.Expected, value)
				assert.NoError(t, err)
			}
		})
	}
}

func TestAllowedValidator_nil_validator(t *testing.T) {
	assert.Panics(t, func() {
		cluster.MakeAllowedValidator(nil, nil).Validate("foo")
	})
}

func TestAllowedValidator_nil_allowed(t *testing.T) {
	value, err := cluster.MakeAllowedValidator(
		validateFunc(func(any) (any, error) {
			return "foo", nil
		}), nil).Validate("foo")

	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"\": "+
		"value \"foo\" should be one of []")
}

func TestAllowedValidator_not_exist(t *testing.T) {
	const input = 123
	calls := 0
	value, err := cluster.MakeAllowedValidator(
		validateFunc(func(value any) (any, error) {
			assert.Equal(t, input, value)
			calls++
			return "not_exists", nil
		}), []any{"foo", "bar", "zoo"}).Validate(input)

	assert.Equal(t, 1, calls)
	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"\": "+
		"value \"123\" should be one of [foo bar zoo]")
}

func TestAllowedValidator_exist(t *testing.T) {
	const input = 123
	value, err := cluster.MakeAllowedValidator(
		validateFunc(func(value any) (any, error) {
			assert.Equal(t, input, value)
			return "zoo", nil
		}), []any{"foo", "bar", "zoo"}).Validate(input)

	assert.Equal(t, "zoo", value)
	assert.NoError(t, err)
}

func TestAllowedValidator_integer(t *testing.T) {
	value, err := cluster.MakeAllowedValidator(cluster.IntegerValidator{},
		[]any{"foo", "bar", 123, "zoo"}).Validate("123")

	assert.Equal(t, 123, value)
	assert.NoError(t, err)
}

func TestAllowedValidator_number(t *testing.T) {
	cases := []struct {
		Value    any
		Allowed  []any
		Expected float64
	}{
		{"1.1", []any{"foo", "zoo", 1.1, "bar"}, float64(1.1)},
		{1.2, []any{"foo", "zoo", 1.2, "bar"}, float64(1.2)},
		{3, []any{"foo", "zoo", 3, "bar"}, float64(3)},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.Value), func(t *testing.T) {
			value, err := cluster.MakeAllowedValidator(
				cluster.NumberValidator{},
				tc.Allowed).Validate(tc.Value)

			assert.Equal(t, tc.Expected, value)
			assert.NoError(t, err)
		})
	}
}

func TestAllowedValidator_string(t *testing.T) {
	value, err := cluster.MakeAllowedValidator(cluster.StringValidator{},
		[]any{"foo", "bar", 1, "zoo"}).Validate("zoo")

	assert.Equal(t, "zoo", value)
	assert.NoError(t, err)
}

func TestArrayValidator_validate(t *testing.T) {
	current := 0
	inputs := []any{}

	value, err := cluster.MakeArrayValidator(
		validateFunc(func(value any) (any, error) {
			inputs = append(inputs, value)
			current++
			return current, nil
		})).Validate([]any{"foo", "bar", "zoo"})

	assert.Equal(t, []any{"foo", "bar", "zoo"}, inputs)
	assert.Equal(t, current, 3)
	assert.Equal(t, []any{1, 2, 3}, value)
	assert.NoError(t, err)
}

func TestArrayValidator_validate_error(t *testing.T) {
	current := 0
	inputs := []any{}

	value, err := cluster.MakeArrayValidator(
		validateFunc(func(value any) (any, error) {
			inputs = append(inputs, value)
			current++
			if current == 2 {
				return nil, fmt.Errorf("foo1")
			}
			return current, nil
		})).Validate([]any{"foo", "bar", "zoo"})

	assert.Equal(t, []any{"foo", "bar", "zoo"}, inputs)
	assert.Equal(t, current, 3)
	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"\": foo1")
}

func TestArrayValidator_validate_errors(t *testing.T) {
	current := 0

	value, err := cluster.MakeArrayValidator(
		validateFunc(func(value any) (any, error) {
			current++
			return nil, fmt.Errorf("foo%d", current)
		})).Validate([]any{"foo", "bar", "zoo"})

	assert.Equal(t, current, 3)
	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"\": foo1\nfoo2\nfoo3")
}

func TestArrayValidator_nil_validator(t *testing.T) {
	assert.Panics(t, func() {
		cluster.MakeArrayValidator(nil).Validate([]any{1, 2, 3})
	})
}

func TestArrayValidator_not_any_array(t *testing.T) {
	cases := []any{
		1,
		"foo",
		[]int{},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			value, err := cluster.MakeArrayValidator(nil).Validate(tc)

			assert.Nil(t, value)
			assert.ErrorContains(t, err, "must be an array")
		})
	}
}

func TestArrayValidator_number(t *testing.T) {
	value, err := cluster.MakeArrayValidator(cluster.NumberValidator{}).
		Validate([]any{"1", 1.1, "123", 1})

	assert.Equal(t,
		[]any{float64(1), float64(1.1), float64(123), float64(1)}, value)
	assert.NoError(t, err)
}

func TestArrayValidator_allowed(t *testing.T) {
	value, err := cluster.MakeArrayValidator(
		cluster.MakeAllowedValidator(cluster.NumberValidator{}, []any{1, 2})).
		Validate([]any{"1", 2})

	assert.Equal(t, []any{float64(1), float64(2)}, value)
	assert.NoError(t, err)
}

func TestRecordValidator_values(t *testing.T) {
	inputs := []any{}

	value, err := cluster.MakeRecordValidator(map[string]cluster.Validator{
		"foo": validateFunc(func(value any) (any, error) {
			inputs = append(inputs, value)
			return 1, nil
		}),
		"zoo": validateFunc(func(value any) (any, error) {
			inputs = append(inputs, value)
			return 2, nil
		}),
		"not_exist": validateFunc(func(value any) (any, error) {
			inputs = append(inputs, value)
			return 3, nil
		}),
	}).Validate(map[any]any{
		"foo":             "foo",
		"zoo":             "zoo",
		"not_exist_input": "not_exist_input",
	})

	assert.ElementsMatch(t, []any{"foo", "zoo"}, inputs)
	assert.Equal(t, map[any]any{
		"foo": 1,
		"zoo": 2,
	}, value)
	assert.NoError(t, err)
}

func TestRecordValidator_empty_validators(t *testing.T) {
	src := map[any]any{
		"foo": "bar",
		"bar": 1,
	}

	value, err := cluster.MakeRecordValidator(nil).Validate(src)

	assert.Equal(t, map[any]any{}, value)
	assert.NoError(t, err)
}

func TestRecordValidator_nil_validator(t *testing.T) {
	assert.Panics(t, func() {
		cluster.MakeRecordValidator(map[string]cluster.Validator{
			"foo": nil,
		}).Validate(map[any]any{
			"foo": "bar",
		})
	})
}

func TestRecordValidator_not_any_map(t *testing.T) {
	cases := []any{
		1,
		"foo",
		[]int{},
		[]any{},
		map[string]any{},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			value, err := cluster.MakeRecordValidator(nil).Validate(tc)

			assert.Nil(t, value)
			assert.ErrorContains(t, err, "must be a map")
		})
	}
}

func TestRecordValidator_errors(t *testing.T) {
	value, err := cluster.MakeRecordValidator(map[string]cluster.Validator{
		"foo": validateFunc(func(value any) (any, error) {
			return nil, fmt.Errorf("foo2")
		}),
		"bar": validateFunc(func(value any) (any, error) {
			return nil, fmt.Errorf("foo1")
		}),
	}).Validate(map[any]any{
		"foo": "zoo",
		"bar": 123,
	})

	assert.Nil(t, value)
	assert.ErrorContains(t, err, "invalid path \"bar\": foo1")
	assert.ErrorContains(t, err, "invalid path \"foo\": foo2")
}

func TestMapValidator_validate(t *testing.T) {
	value, err := cluster.MakeMapValidator(
		validateFunc(func(value any) (any, error) {
			str := value.(string)
			return str + str, nil
		}),
		validateFunc(func(value any) (any, error) {
			str := value.(string)
			return str + str + str, nil
		}),
	).Validate(map[any]any{
		"foo": "bar",
		"zoo": "foo",
	})

	assert.Equal(t, map[any]any{
		"foofoo": "barbarbar",
		"zoozoo": "foofoofoo",
	}, value)
	assert.NoError(t, err)
}

func TestMapValidator_nil_key_validator(t *testing.T) {
	assert.Panics(t, func() {
		cluster.MakeMapValidator(
			nil, validateFunc(func(value any) (any, error) {
				return 1, nil
			})).Validate(map[any]any{
			"foo": "bar",
		})
	})
}

func TestMapValidator_nil_value_validator(t *testing.T) {
	assert.Panics(t, func() {
		cluster.MakeMapValidator(
			validateFunc(func(value any) (any, error) {
				return 1, nil
			}), nil).Validate(map[any]any{
			"foo": "bar",
		})
	})
}

func TestMapValidator_invalid_key(t *testing.T) {
	value, err := cluster.MakeMapValidator(
		validateFunc(func(value any) (any, error) {
			if value.(string) == "foo" {
				return nil, fmt.Errorf("foo1")
			}
			return value, nil
		}),
		validateFunc(func(value any) (any, error) {
			return value, nil
		}),
	).Validate(map[any]any{
		"foo": "bar",
		"zoo": 1,
	})

	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"\": foo1")
}

func TestMapValidator_invalid_value(t *testing.T) {
	value, err := cluster.MakeMapValidator(
		validateFunc(func(value any) (any, error) {
			return value, nil
		}),
		validateFunc(func(value any) (any, error) {
			if value.(string) == "foo" {
				return nil, fmt.Errorf("foo1")
			}
			return value, nil
		}),
	).Validate(map[any]any{
		"foo": "bar",
		"zoo": "foo",
	})

	assert.Nil(t, value)
	assert.EqualError(t, err, "invalid path \"zoo\": foo1")
}

func TestMapValidator_invalid_multiple(t *testing.T) {
	value, err := cluster.MakeMapValidator(
		validateFunc(func(value any) (any, error) {
			if value.(string) == "foo" {
				return nil, fmt.Errorf("foo1")
			}
			return value, nil
		}),
		validateFunc(func(value any) (any, error) {
			if value.(string) == "foo" {
				return nil, fmt.Errorf("foo2")
			}
			return value, nil
		}),
	).Validate(map[any]any{
		"foo": "bar",
		"zoo": "foo",
	})

	assert.Nil(t, value)
	assert.ErrorContains(t, err, "invalid path \"\": foo1")
	assert.ErrorContains(t, err, "invalid path \"zoo\": foo2")
}

func TestMapValidator_not_any_map(t *testing.T) {
	cases := []any{
		1,
		"foo",
		[]int{},
		[]any{},
		map[string]any{},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			value, err := cluster.MakeMapValidator(nil, nil).Validate(tc)

			assert.Nil(t, value)
			assert.ErrorContains(t, err, "must be a map")
		})
	}
}

func TestMapValidator_string_array_allowed(t *testing.T) {
	value, err := cluster.MakeMapValidator(
		cluster.StringValidator{},
		cluster.MakeArrayValidator(
			cluster.MakeAllowedValidator(cluster.StringValidator{},
				[]any{"1", "2"}),
		),
	).Validate(map[any]any{
		"foo": []any{1},
	})

	assert.Equal(t, map[any]any{"foo": []any{"1"}}, value)
	assert.NoError(t, err)
}

func TestMapValidator_interger_record(t *testing.T) {
	value, err := cluster.MakeMapValidator(
		cluster.IntegerValidator{},
		cluster.MakeRecordValidator(map[string]cluster.Validator{
			"foo": cluster.IntegerValidator{},
			"bar": cluster.StringValidator{},
		}),
	).Validate(map[any]any{
		"9": map[any]any{
			"foo": "1",
			"bar": 3,
			"zoo": 4,
		},
	})

	assert.Equal(t, map[any]any{9: map[any]any{"foo": 1, "bar": "3"}}, value)
	assert.NoError(t, err)
}
