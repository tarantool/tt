package formatter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeScalarFloat(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"float64", float64(1.25), "1.25"},
		{"float64 precision", float64(1.23456789012345), "1.23456789012345"},
		{"float32", float32(1.25), "1.25"},
		{"float32 decimal", float32(0.1), "0.1"},
		{"float32 negative", float32(-2.5), "-2.5"},
		{"float32 zero", float32(0), "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeScalar(tc.value); got != tc.want {
				t.Errorf("encodeScalar(%T(%v)) = %q, want %q", tc.value, tc.value, got, tc.want)
			}
		})
	}
}

func TestRenderArraysRejectsInvalidRows(t *testing.T) {
	cases := []struct {
		name  string
		batch []any
		want  string
	}{
		{"scalar", []any{42}, "expected an array, got int"},
		{"nil", []any{nil}, "expected an array, got <nil>"},
		{"mixed", []any{[]any{1, 2}, "invalid row"}, "expected an array, got string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, transpose := range []bool{false, true} {
				output, err := renderArrays(tc.batch, transpose, Opts{})
				require.EqualError(t, err, tc.want)
				require.Empty(t, output)
			}
		})
	}
}
