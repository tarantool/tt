package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// formBuilder writes a canonical form by hand, as the top of hash.go lays it
// out, to check the encoder against.
type formBuilder []byte

func (f formBuilder) u64(n uint64) formBuilder { return binary.BigEndian.AppendUint64(f, n) }

func (f formBuilder) tag(tag byte) formBuilder { return append(f, tag) }

func (f formBuilder) key(key string) formBuilder {
	return append(f.u64(uint64(len(key))), key...)
}

func (f formBuilder) text(tag byte, text string) formBuilder {
	return append(f.tag(tag).u64(uint64(len(text))), text...)
}

// TestCanonicalFormKnownAnswer pins the canonical form, version 1, byte for
// byte: a lock written by one tt has to be current for every later one that
// writes the same scheme.
func TestCanonicalFormKnownAnswer(t *testing.T) {
	t.Parallel()

	const doc = `
s = 'x'
i = -1
f = 1.5
b = true
o = 1979-05-27T00:32:00.5-07:00
l = 1979-05-27T07:32:00
d = 1979-05-27
c = 07:32:00.25
a = [1, 'y']
t = {}
`

	var want formBuilder

	want = want.tag('t').u64(10).
		key("a").tag('a').u64(2).tag('i').u64(1).text('s', "y").
		key("b").tag('b')
	want = append(want, 1)
	want = want.
		key("c").text('c', "07:32:00.25").
		key("d").text('d', "1979-05-27").
		key("f").tag('f').u64(0x3ff8000000000000).
		key("i").tag('i').u64(0xffffffffffffffff).
		key("l").text('l', "1979-05-27T07:32:00").
		key("o").text('o', "1979-05-27T00:32:00.5-07:00").
		key("s").text('s', "x").
		key("t").tag('t').u64(0)

	var tree map[string]any

	require.NoError(t, toml.Unmarshal([]byte(doc), &tree))

	form, err := canonicalForm(tree)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(want), hex.EncodeToString(form))

	sum := sha256.Sum256(want)

	hash, err := HashTOML([]byte(doc))
	require.NoError(t, err)
	assert.Equal(t, "toml1-sha256:"+hex.EncodeToString(sum[:]), hash)
}

// TestCanonicalFormCoversWhatGoTOMLDecodes pins the Go types go-toml decodes
// each TOML type into when it has no struct to fill: they are the types the
// form has a tag for. A go-toml that changed one fails here, and not only on
// the first manifest that holds such a value.
func TestCanonicalFormCoversWhatGoTOMLDecodes(t *testing.T) {
	t.Parallel()

	const doc = `
s = 'x'
i = 1
f = 1.0
b = true
o = 1979-05-27T07:32:00Z
l = 1979-05-27T07:32:00
d = 1979-05-27
c = 07:32:00
a = [1]
t = {k = 1}
at = [{k = 1}]
`

	var tree map[string]any

	require.NoError(t, toml.Unmarshal([]byte(doc), &tree))

	want := map[string]any{
		"s": "", "i": int64(0), "f": float64(0), "b": false,
		"o": time.Time{}, "l": toml.LocalDateTime{}, "d": toml.LocalDate{}, "c": toml.LocalTime{},
		"a": []any{}, "t": map[string]any{}, "at": []any{},
	}
	for key, sample := range want {
		assert.IsType(t, sample, tree[key], key)
	}

	tables, ok := tree["at"].([]any)
	require.True(t, ok)
	assert.IsType(t, map[string]any{}, tables[0])

	_, err := canonicalForm(tree)
	require.NoError(t, err)
}

// TestCanonicalFormRefusesWhatItHasNoTagFor pins that a value of a Go type
// the form has no tag for fails the hash, naming where it is, instead of
// being skipped.
func TestCanonicalFormRefusesWhatItHasNoTagFor(t *testing.T) {
	t.Parallel()

	values := map[string]any{
		"int":               1,
		"uint64":            uint64(1),
		"float32":           float32(1),
		"nil":               nil,
		"[]string":          []string{"x"},
		"map[string]string": map[string]string{},
		"*time.Time":        &time.Time{},
		"struct":            struct{}{},
	}

	for name, value := range values {
		_, err := canonicalForm(map[string]any{"t": map[string]any{"a": []any{value}}})
		require.ErrorIs(t, err, errHashType, name)
		assert.Contains(t, err.Error(), "t.a[0]", name)
	}

	// TOML writes offsets in whole minutes; one that is not is refused too.
	odd := time.Date(1979, 5, 27, 7, 32, 0, 0, time.FixedZone("", 30))
	_, err := canonicalForm(map[string]any{"o": odd})
	require.ErrorIs(t, err, errHashOffset)
}

// TestCanonicalFormOfFloats pins the floats the text of a document cannot
// tell apart from the decoded value: every NaN is one, and the zeros are two.
func TestCanonicalFormOfFloats(t *testing.T) {
	t.Parallel()

	form := func(value float64) string {
		t.Helper()

		out, err := canonicalForm(map[string]any{"f": value})
		require.NoError(t, err)

		return hex.EncodeToString(out)
	}

	assert.Equal(t, form(math.NaN()), form(math.Float64frombits(0xfff8000000000002)))
	assert.Equal(t, form(math.NaN()), form(math.Float64frombits(0x7ff0000000000001)))
	assert.NotEqual(t, form(0), form(math.Copysign(0, -1)))
	assert.NotEqual(t, form(math.Inf(1)), form(math.Inf(-1)))
}
