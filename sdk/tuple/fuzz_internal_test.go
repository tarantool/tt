package tuple

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jsonNumberSeeds are texts of numbers and of what looks like one.
var jsonNumberSeeds = []string{
	"0", "-0", "1", "-1", "007", "0.5", "-0.0", "1e3", "1E+3", "1e-3", "1.", ".5", "+1", "1e",
	"18446744073709551615", "18446744073709551616", "-9223372036854775808",
	"-9223372036854775809", "1e400", "-1e400", "0e0", "00", "-", "", "1 ", " 1", "0x1",
	"Infinity", "NaN", "1_000", "123456789012345678901234567890",
}

// FuzzIsJSONNumber checks isJSONNumber against encoding/json and jsonNumber
// against an oracle of big integers: an integer is a uint64, or an int64
// when it is negative, and any other number a float64.
func FuzzIsJSONNumber(f *testing.F) {
	for _, seed := range jsonNumberSeeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		require.Equal(t, isNumberOfJSON(text), isJSONNumber(text), "%q", text)

		if !isJSONNumber(text) {
			return
		}

		value, err := jsonNumber(text)

		if strings.ContainsAny(text, ".eE") {
			want, wantErr := strconv.ParseFloat(text, float64Bits)
			if wantErr == nil {
				// A number too small for a float64, which strconv reads as 0.
				wantErr = checkUnderflow(want, text)
			}

			if wantErr != nil {
				require.Error(t, err, "%q", text)

				return
			}

			require.NoError(t, err)
			require.Equal(t, math.Float64bits(want), math.Float64bits(asFloat(t, value)), text)

			return
		}

		integer, ok := new(big.Int).SetString(text, decimalBase)
		require.True(t, ok, text)

		switch {
		case integer.Sign() >= 0 && integer.IsUint64():
			require.NoError(t, err)
			require.Equal(t, integer.Uint64(), value, text)
		case integer.Sign() < 0 && integer.IsInt64():
			require.NoError(t, err)
			require.Equal(t, integer.Int64(), value, text)
		default:
			want, wantErr := strconv.ParseFloat(text, float64Bits)
			if wantErr != nil {
				require.Error(t, err, "%q", text)

				return
			}

			require.NoError(t, err)
			require.Equal(t, math.Float64bits(want), math.Float64bits(asFloat(t, value)), text)
		}
	})
}

func asFloat(t *testing.T, value any) float64 {
	t.Helper()

	number, ok := value.(float64)
	require.True(t, ok, "%#v is not a float64", value)

	return number
}

// isNumberOfJSON reports whether encoding/json reads text, with no space
// around it, as one number.
func isNumberOfJSON(text string) bool {
	if text == "" || strings.TrimSpace(text) != text || !json.Valid([]byte(text)) {
		return false
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()

	token, err := dec.Token()
	if err != nil {
		return false
	}

	_, ok := token.(json.Number)

	return ok
}

// FuzzParseDatetimeStrict checks that the strict parse of a datetime, which
// the guess of the type of a value takes, reads what the parse reads when the
// text has a time and a time zone, and nothing else.
func FuzzParseDatetimeStrict(f *testing.F) {
	for _, seed := range []string{
		"2024-11-14", "2024-11-14T14:02", "2024-11-14T14:02Z", "2024-11-14 Europe/Moscow",
		"2024-11-14T14:02:36 MSK", "2024-11-14T14:02:36A", "2024-11-14T14:02:36+0100 CET",
		"2024-11-14 14:02:36", "2024-11-14T14:02:36.5-01:30",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		loose, looseErr := parseDatetime(text, false)

		strict, strictErr := parseDatetime(text, true)
		if strictErr != nil {
			require.ErrorIs(t, strictErr, ErrDatetime)

			if looseErr == nil {
				parts, ok := scanDatetime(text)
				require.True(t, ok, text)
				require.False(t, parts.hasTime && parts.hasZone,
					"%q has a time and a zone, but the strict parse fails: %v", text, strictErr)
			}

			return
		}

		require.NoError(t, looseErr, text)
		require.True(t, sameDatetime(loose, strict), text)

		parts, ok := scanDatetime(text)
		require.True(t, ok, text)
		require.True(t, parts.hasTime && parts.hasZone, text)
	})
}
