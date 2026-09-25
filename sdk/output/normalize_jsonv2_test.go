//go:build goexperiment.jsonv2 && go1.27

package output_test

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/output"
)

// The spelling of non-finite floats is checked against the standard
// library itself: jsontext.Float and jsontext.Float32 are the tokens
// encoding/json/v2 writes for a float under format:nonfinite. This file
// needs encoding/json/jsontext as Go 1.27 ships it, so it builds only with
// a Go 1.27 toolchain and the jsonv2 experiment on, as it is by default
// there; the module's go directive stays below 1.27.

// tokenObject returns the bytes of {"v": value}, value written as token.
func tokenObject(t *testing.T, value jsontext.Token) []byte {
	t.Helper()

	var buf bytes.Buffer

	encoder := jsontext.NewEncoder(&buf)
	for _, token := range []jsontext.Token{
		jsontext.BeginObject, jsontext.String("v"), value, jsontext.EndObject,
	} {
		require.NoError(t, encoder.WriteToken(token))
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// normalizedObject returns the bytes encoding/json writes for {"v": value}
// once normalised.
func normalizedObject(t *testing.T, value any) []byte {
	t.Helper()

	normalized, err := output.Normalize(map[string]any{"v": value})
	require.NoError(t, err)

	encoded, err := json.Marshal(normalized)
	require.NoError(t, err)

	return encoded
}

func TestNonFiniteMatchesJSONv2(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{
		math.NaN(), math.Inf(1), math.Inf(-1), 1.5, 0, math.Copysign(0, -1),
	} {
		assert.Equal(t, string(tokenObject(t, jsontext.Float(value))),
			string(normalizedObject(t, value)), "float64 %v", value)
	}

	for _, value := range []float32{
		float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)), 0.1, 2,
	} {
		assert.Equal(t, string(tokenObject(t, jsontext.Float32(value))),
			string(normalizedObject(t, value)), "float32 %v", value)
	}
}

func TestNonFiniteKeysMatchJSONv2(t *testing.T) {
	t.Parallel()

	for _, key := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		normalized, err := output.Normalize(map[any]any{key: true})
		require.NoError(t, err)

		assert.Equal(t, map[string]any{jsontext.Float(key).String(): true}, normalized)
	}

	for _, key := range []float32{
		float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)),
	} {
		normalized, err := output.Normalize(map[any]any{key: true})
		require.NoError(t, err)

		assert.Equal(t, map[string]any{jsontext.Float32(key).String(): true}, normalized)
	}
}
