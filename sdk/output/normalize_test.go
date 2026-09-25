package output_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/output"
)

// The values below mirror what go-tarantool's decoder yields for a tuple:
// every MessagePack map is a map[any]any, even one keyed by strings, and an
// integer comes out in the narrowest type its wire encoding names (int8,
// uint16, ...). A MessagePack bin is a []byte.

// textID stands in for uuid.UUID: an array type that marshals as text.
type textID [4]byte

func (id textID) MarshalText() ([]byte, error) {
	return []byte(hex.EncodeToString(id[:])), nil
}

// rawTuple marshals itself, so Normalize must not look inside it even
// though it is a map keyed by interfaces.
type rawTuple map[any]any

func (rawTuple) MarshalJSON() ([]byte, error) { return []byte(`"raw"`), nil }

// ptrMarshaler marshals itself only through a pointer.
type ptrMarshaler struct {
	Tuple map[any]any
}

func (*ptrMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"ptr"`), nil }

// holder has a ptrMarshaler field, addressable when holder is reached
// through a pointer, as encoding/json then calls the pointer method.
type holder struct {
	Marshaler ptrMarshaler
}

// stamp stands in for go-tarantool's datetime.Datetime: no exported fields,
// so encoding/json renders it as {}, and a String form.
type stamp struct {
	at time.Time
}

func (s stamp) String() string { return s.at.Format(time.RFC3339) }

// row is a result that carries decoded tuples in fields of type any.
type row struct {
	Space  string `json:"space"`
	Tuple  any    `json:"tuple"`
	Tuples []any  `json:"tuples"`
	Hidden any    `json:"-"`
}

// typedRow declares a field of the decoded map type itself.
type typedRow struct {
	Tuple map[any]any `json:"tuple"`
}

// Embedded is embedded in wrapper to check promoted fields are walked.
type Embedded struct {
	Inner any `json:"inner"`
}

type wrapper struct {
	Embedded
}

// node links to itself to form a cycle.
type node struct {
	Next *node
	Data any
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	nested := map[any]any{"k": int8(1)}

	tests := []struct {
		name  string
		input func() any
		want  any
	}{
		{
			name:  "string keys",
			input: func() any { return map[any]any{"name": "x", "n": int8(1)} },
			want:  map[string]any{"name": "x", "n": int8(1)},
		},
		{
			name:  "integer keys",
			input: func() any { return map[any]any{int8(1): "a", uint16(300): "b", int8(-5): "c"} },
			want:  map[string]any{"1": "a", "300": "b", "-5": "c"},
		},
		{
			name:  "bool and float keys",
			input: func() any { return map[any]any{true: 1, 2.5: 2, float32(0.1): 3} },
			want:  map[string]any{"true": 1, "2.5": 2, "0.1": 3},
		},
		{
			name:  "text marshaler key",
			input: func() any { return map[any]any{textID{1, 2, 3, 4}: "id"} },
			want:  map[string]any{"01020304": "id"},
		},
		{
			name: "maps nested in slices",
			input: func() any {
				return []any{
					map[any]any{"k": int8(1)},
					map[any]any{int8(2): []any{map[any]any{int8(3): "x"}}},
				}
			},
			want: []any{
				map[string]any{"k": int8(1)},
				map[string]any{"2": []any{map[string]any{"3": "x"}}},
			},
		},
		{
			name:  "typed slice of maps becomes []any",
			input: func() any { return []map[any]any{{int8(1): "a"}} },
			want:  []any{map[string]any{"1": "a"}},
		},
		{
			name:  "string-keyed map of maps gets any values",
			input: func() any { return map[string]map[any]any{"t": {int8(1): "a"}} },
			want:  map[string]any{"t": map[string]any{"1": "a"}},
		},
		{
			name:  "integer-keyed map keeps its key type",
			input: func() any { return map[int]any{7: map[any]any{"k": "v"}} },
			want:  map[int]any{7: map[string]any{"k": "v"}},
		},
		{
			name:  "array",
			input: func() any { return [2]any{map[any]any{int8(1): "a"}, "b"} },
			want:  [2]any{map[string]any{"1": "a"}, "b"},
		},
		{
			name:  "array of maps becomes []any",
			input: func() any { return [1]map[any]any{{int8(1): "a"}} },
			want:  []any{map[string]any{"1": "a"}},
		},
		{
			name:  "bytes stay bytes",
			input: func() any { return map[any]any{"bin": []byte("hi\x00\xff")} },
			want:  map[string]any{"bin": []byte("hi\x00\xff")},
		},
		{
			name:  "json marshaler is not walked",
			input: func() any { return []any{rawTuple{int8(1): "a"}} },
			want:  []any{rawTuple{int8(1): "a"}},
		},
		{
			name:  "text marshaler value is not walked",
			input: func() any { return map[any]any{"id": textID{1, 2, 3, 4}} },
			want:  map[string]any{"id": textID{1, 2, 3, 4}},
		},
		{
			name:  "opaque stringer becomes its string",
			input: func() any { return map[any]any{"at": stamp{at: day}} },
			want:  map[string]any{"at": "2026-09-25T10:00:00Z"},
		},
		{
			name:  "nil",
			input: func() any { return nil },
			want:  nil,
		},
		{
			name: "nil values",
			input: func() any {
				return map[any]any{"a": nil, "m": map[any]any(nil), "s": []any(nil)}
			},
			want: map[string]any{"a": nil, "m": map[string]any(nil), "s": []any(nil)},
		},
		{
			name:  "nil pointer",
			input: func() any { return (*row)(nil) },
			want:  (*row)(nil),
		},
		{
			name:  "pointer to a map is dereferenced",
			input: func() any { return &map[any]any{int8(1): "a"} },
			want:  map[string]any{"1": "a"},
		},
		{
			name: "pointer to a struct stays a pointer",
			input: func() any {
				return &row{Space: "s", Tuple: map[any]any{int8(1): "a"}, Tuples: nil, Hidden: nil}
			},
			want: &row{Space: "s", Tuple: map[string]any{"1": "a"}, Tuples: nil, Hidden: nil},
		},
		{
			name: "struct fields of type any",
			input: func() any {
				return row{
					Space:  "s",
					Tuple:  map[any]any{int8(1): "a"},
					Tuples: []any{[]any{uint16(300), map[any]any{"k": true}}},
					Hidden: map[any]any{nil: "not walked"},
				}
			},
			want: row{
				Space:  "s",
				Tuple:  map[string]any{"1": "a"},
				Tuples: []any{[]any{uint16(300), map[string]any{"k": true}}},
				Hidden: map[any]any{nil: "not walked"},
			},
		},
		{
			name:  "exported embedded struct",
			input: func() any { return wrapper{Embedded{Inner: map[any]any{int8(1): "a"}}} },
			want:  wrapper{Embedded{Inner: map[string]any{"1": "a"}}},
		},
		{
			name:  "pointer marshaler",
			input: func() any { return &ptrMarshaler{Tuple: map[any]any{int8(1): "a"}} },
			want:  &ptrMarshaler{Tuple: map[any]any{int8(1): "a"}},
		},
		{
			name: "pointer marshaler field reached through a pointer",
			input: func() any {
				return &holder{Marshaler: ptrMarshaler{Tuple: map[any]any{int8(1): "a"}}}
			},
			want: &holder{Marshaler: ptrMarshaler{Tuple: map[any]any{int8(1): "a"}}},
		},
		{
			name:  "shared reference is not a cycle",
			input: func() any { return []any{nested, nested} },
			want:  []any{map[string]any{"k": int8(1)}, map[string]any{"k": int8(1)}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := test.input()

			got, err := output.Normalize(input)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.input(), input, "the input is not mutated")
		})
	}
}

func TestNormalizeNonFinite(t *testing.T) {
	t.Parallel()

	nan, inf := math.NaN(), math.Inf(1)
	nan32, inf32 := float32(nan), float32(inf)

	tests := []struct {
		name  string
		input func() any
		want  any
	}{
		{
			name:  "float64 values",
			input: func() any { return []any{nan, inf, -inf, 1.5, 0.0} },
			want:  []any{"NaN", "Infinity", "-Infinity", 1.5, 0.0},
		},
		{
			name:  "float32 values",
			input: func() any { return []any{nan32, inf32, -inf32, float32(0.1)} },
			want:  []any{"NaN", "Infinity", "-Infinity", float32(0.1)},
		},
		{
			name:  "typed float64 slice becomes []any",
			input: func() any { return []float64{1, nan} },
			want:  []any{1.0, "NaN"},
		},
		{
			name:  "typed float32 array becomes []any",
			input: func() any { return [2]float32{-inf32, 2} },
			want:  []any{"-Infinity", float32(2)},
		},
		{
			name:  "finite floats need no change",
			input: func() any { return []float64{1, 2.5} },
			want:  []float64{1, 2.5},
		},
		{
			name:  "string-keyed map of floats gets any values",
			input: func() any { return map[string]float64{"a": nan, "b": 2} },
			want:  map[string]any{"a": "NaN", "b": 2.0},
		},
		{
			name: "nested in maps and slices",
			input: func() any {
				return map[any]any{"t": map[any]any{int8(1): []any{inf, []any{nan32}}}}
			},
			want: map[string]any{"t": map[string]any{"1": []any{"Infinity", []any{"NaN"}}}},
		},
		{
			name:  "struct field of type any",
			input: func() any { return row{Space: "s", Tuple: -inf, Tuples: nil, Hidden: nil} },
			want:  row{Space: "s", Tuple: "-Infinity", Tuples: nil, Hidden: nil},
		},
		{
			name:  "keys",
			input: func() any { return map[any]any{nan: 1, inf: 2, -inf32: 3, 2.5: 4} },
			want:  map[string]any{"NaN": 1, "Infinity": 2, "-Infinity": 3, "2.5": 4},
		},
		{
			name:  "float-keyed map",
			input: func() any { return map[float32]string{nan32: "a", -inf32: "b", 0.5: "c"} },
			want:  map[string]any{"NaN": "a", "-Infinity": "b", "0.5": "c"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := test.input()

			got, err := output.Normalize(input)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)

			// NaN is not equal to itself, so the input is compared in its
			// printed form; none of these inputs holds a pointer.
			assert.Equal(t, fmt.Sprintf("%#v", test.input()), fmt.Sprintf("%#v", input),
				"the input is not mutated")
		})
	}
}

func TestNormalizeUnchangedIsShared(t *testing.T) {
	t.Parallel()

	tuple := map[string]any{"k": []any{int8(1), "x"}}

	got, err := output.Normalize(tuple)
	require.NoError(t, err)

	got.(map[string]any)["added"] = true

	assert.Contains(t, tuple, "added", "a value needing no change is returned as it is")
}

func TestNormalizeRefuses(t *testing.T) {
	t.Parallel()

	cycle := &node{Next: nil, Data: "x"}

	cycle.Next = cycle

	selfMap := map[string]any{}

	selfMap["self"] = selfMap

	tests := []struct {
		name    string
		input   any
		wantErr error
		wantMsg string
	}{
		{
			name:    "integer and string key collide",
			input:   map[any]any{int8(1): "int", "1": "str"},
			wantErr: output.ErrKeyCollision,
			wantMsg: `map keys collide: "1" (string) and 1 (int8) are both "1"`,
		},
		{
			name:    "two integer widths collide",
			input:   map[any]any{int8(1): "a", uint16(1): "b"},
			wantErr: output.ErrKeyCollision,
			wantMsg: `map keys collide: 1 (int8) and 1 (uint16) are both "1"`,
		},
		{
			name:    "collision deep inside",
			input:   []any{map[any]any{"t": map[any]any{true: 1, "true": 2}}},
			wantErr: output.ErrKeyCollision,
			wantMsg: `map keys collide: "true" (string) and true (bool) are both "true"`,
		},
		{
			name:    "NaN key and its spelling collide",
			input:   map[any]any{math.NaN(): 1, "NaN": 2},
			wantErr: output.ErrKeyCollision,
			wantMsg: `map keys collide: "NaN" (string) and NaN (float64) are both "NaN"`,
		},
		{
			name:    "two NaN keys collide",
			input:   map[any]any{math.NaN(): 1, math.NaN(): 2},
			wantErr: output.ErrKeyCollision,
			wantMsg: `map keys collide: NaN (float64) and NaN (float64) are both "NaN"`,
		},
		{
			name:    "infinities of two widths collide",
			input:   map[any]any{math.Inf(1): 1, float32(math.Inf(1)): 2},
			wantErr: output.ErrKeyCollision,
			wantMsg: `map keys collide: +Inf (float32) and +Inf (float64) are both "Infinity"`,
		},
		{
			name:    "non-finite value in a float field",
			input:   struct{ Ratio float64 }{Ratio: math.NaN()},
			wantErr: output.ErrNotNormalizable,
			wantMsg: "value cannot be normalised: field Ratio of type float64 cannot hold " +
				"a string; declare it as any",
		},
		{
			name:    "nil key",
			input:   map[any]any{nil: 1},
			wantErr: output.ErrNotNormalizable,
			wantMsg: "value cannot be normalised: a nil map key",
		},
		{
			name:    "key with no string form",
			input:   map[any]any{struct{ A int }{1}: 1},
			wantErr: output.ErrNotNormalizable,
			wantMsg: "value cannot be normalised: a map key of type struct { A int } " +
				"has no string form",
		},
		{
			name:    "typed struct field",
			input:   typedRow{Tuple: map[any]any{int8(1): "a"}},
			wantErr: output.ErrNotNormalizable,
			wantMsg: "value cannot be normalised: field Tuple of type map[interface {}]" +
				"interface {} cannot hold a map[string]interface {}; declare it as any",
		},
		{
			name:    "pointer cycle",
			input:   cycle,
			wantErr: output.ErrNotNormalizable,
			wantMsg: "field Next: value cannot be normalised: a reference cycle through " +
				"*output_test.node",
		},
		{
			name:    "map cycle",
			input:   selfMap,
			wantErr: output.ErrNotNormalizable,
			wantMsg: "value cannot be normalised: a reference cycle through " +
				"map[string]interface {}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Repeated, because Go randomises map iteration: the error
			// must not depend on it.
			for range 20 {
				_, err := output.Normalize(test.input)
				require.ErrorIs(t, err, test.wantErr)
				require.EqualError(t, err, test.wantMsg)
			}
		})
	}
}

// tupleResult carries a decoded tuple as the result of a command.
type tupleResult struct {
	Tuple any `json:"tuple"`
}

func (tupleResult) Human(io.Writer) error { return nil }

func TestEmitJSONNormalizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tuple   any
		want    string
		wantErr error
	}{
		{
			name:  "integer keys",
			tuple: map[any]any{int8(1): "a", uint16(300): map[any]any{"k": int8(2)}},
			want: "{\n  \"tuple\": {\n    \"1\": \"a\",\n" +
				"    \"300\": {\n      \"k\": 2\n    }\n  }\n}\n",
		},
		{
			name:  "bool and float keys",
			tuple: map[any]any{true: 1, 2.5: 2},
			want:  "{\n  \"tuple\": {\n    \"2.5\": 2,\n    \"true\": 1\n  }\n}\n",
		},
		{
			name:  "bytes as base64",
			tuple: []any{[]byte("hi\x00\xff")},
			want:  "{\n  \"tuple\": [\n    \"aGkA/w==\"\n  ]\n}\n",
		},
		{
			name:  "non-finite values",
			tuple: []any{math.NaN(), math.Inf(1), float32(math.Inf(-1)), 1.5},
			want: "{\n  \"tuple\": [\n    \"NaN\",\n    \"Infinity\",\n" +
				"    \"-Infinity\",\n    1.5\n  ]\n}\n",
		},
		{
			name:  "non-finite keys",
			tuple: map[any]any{math.Inf(-1): math.NaN()},
			want:  "{\n  \"tuple\": {\n    \"-Infinity\": \"NaN\"\n  }\n}\n",
		},
		{
			name:    "collision",
			tuple:   map[any]any{int8(1): "int", "1": "str"},
			wantErr: output.ErrKeyCollision,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			streams, stdout, _ := newStreams()

			printer, err := output.NewPrinter(streams, output.FormatJSON)
			require.NoError(t, err)

			err = printer.Emit(tupleResult{Tuple: test.tuple})
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				assert.Empty(t, stdout.String())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, stdout.String())
			assert.True(t, json.Valid(bytes.TrimSpace(stdout.Bytes())))
		})
	}
}
