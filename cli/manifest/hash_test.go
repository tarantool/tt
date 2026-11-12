package manifest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// hashTOML is manifest.HashTOML of a document that has to hash.
func hashTOML(t *testing.T, doc string) string {
	t.Helper()

	hash, err := manifest.HashTOML([]byte(doc))
	require.NoError(t, err)

	return hash
}

// TestHashTOMLIgnoresHowTheFileSaysIt pins that two spellings of one tree hash
// alike: the hash is of what a manifest says, so none of these edits makes a
// lock stale.
func TestHashTOMLIgnoresHowTheFileSaysIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b string
	}{
		{"a comment", "a = 1\n", "# head\na = 1 # tail\n# foot\n"},
		{"blank lines and indentation",
			"[t]\na = 1\nb = 2\n", "\n\n  [t]\n\ta   =   1\n\n    b=2\n"},
		{"the order of keys", "a = 1\nb = 'x'\n", "b = 'x'\na = 1\n"},
		{"the order of tables", "[x]\na = 1\n[y]\nb = 2\n", "[y]\nb = 2\n[x]\na = 1\n"},
		{"an inline table", "[t]\na = 1\nb = 2\n", "t = {b = 2, a = 1}\n"},
		{"dotted keys", "[t.u]\na = 1\n", "t.u.a = 1\n"},
		{"dotted keys in a table", "[t]\nu.a = 1\n", "t = {u = {a = 1}}\n"},
		{"a basic quoted key", "name = 1\n", `"name" = 1` + "\n"},
		{"a literal quoted key", "name = 1\n", "'name' = 1\n"},
		{"a literal string", `a = "x\\y"` + "\n", `a = 'x\y'` + "\n"},
		{"a multi-line string", `a = "x\ny"` + "\n", "a = \"\"\"\nx\ny\"\"\"\n"},
		{"a multi-line literal string", "a = 'x'\n", "a = '''x'''\n"},
		{"an escape", "a = \"x\ty\"\n", `a = "x\ty"` + "\n"},
		{"a Unicode escape", "a = \"é\"\n", `a = "\U000000e9"` + "\n"},
		{"a line-ending backslash", `a = "xy"` + "\n", "a = \"\"\"x\\\n   y\"\"\"\n"},
		{"a hexadecimal integer", "a = 16\n", "a = 0x10\n"},
		{"an octal integer", "a = 16\n", "a = 0o20\n"},
		{"a binary integer", "a = 16\n", "a = 0b10000\n"},
		{"a signed integer", "a = 16\n", "a = +16\n"},
		{"underscores of an integer", "a = 1000\n", "a = 1_000\n"},
		{"an exponent", "a = 100.0\n", "a = 1e2\n"},
		{"a signed exponent", "a = 100.0\n", "a = 1E+2\n"},
		{"zeros of a float", "a = 100.0\n", "a = 100.000\n"},
		{"underscores of a float", "a = 100.0\n", "a = 1_00.0\n"},
		{"the sign of nan", "a = nan\n", "a = -nan\n"},
		{"a signed nan", "a = nan\n", "a = +nan\n"},
		{"a signed inf", "a = inf\n", "a = +inf\n"},
		{"the separator of a date-time",
			"a = 1979-05-27T07:32:00Z\n", "a = 1979-05-27 07:32:00z\n"},
		{"a zero offset", "a = 1979-05-27T07:32:00Z\n", "a = 1979-05-27T07:32:00+00:00\n"},
		{"a negative zero offset", "a = 1979-05-27T07:32:00Z\n", "a = 1979-05-27T07:32:00-00:00\n"},
		{"the zeros of a fraction",
			"a = 1979-05-27T07:32:00.5Z\n", "a = 1979-05-27T07:32:00.500Z\n"},
		{"a zero fraction", "a = 07:32:00\n", "a = 07:32:00.000\n"},
		{"a local date-time", "a = 1979-05-27T07:32:00.25\n", "a = 1979-05-27 07:32:00.250000\n"},
		{"an array of tables", "[[a]]\nb = 1\n[[a]]\nb = 2\n", "a = [{b = 1}, {b = 2}]\n"},
		{"the layout of an array", "a = [1, 2]\n", "a = [\n  1, # one\n  2,\n]\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, hashTOML(t, tc.a), hashTOML(t, tc.b))
		})
	}
}

// TestHashTOMLTellsTreesApart pins that two trees never hash alike: a type, a
// value, a key or the order of an array that changed is a change, and so is
// where one value ends and the next begins.
func TestHashTOMLTellsTreesApart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b string
	}{
		{"an integer and a float", "a = 1\n", "a = 1.0\n"},
		{"a string and an integer", "a = '1'\n", "a = 1\n"},
		{"a string and a boolean", "a = 'true'\n", "a = true\n"},
		{"true and false", "a = true\n", "a = false\n"},
		{"a string and a date", "a = '1979-05-27'\n", "a = 1979-05-27\n"},
		{"a string and a local time", "a = '07:32:00'\n", "a = 07:32:00\n"},
		{"a local and an offset date-time",
			"a = 1979-05-27T07:32:00\n", "a = 1979-05-27T07:32:00Z\n"},
		{"a local date-time and a local date", "a = 1979-05-27T00:00:00\n", "a = 1979-05-27\n"},
		{"one instant at two offsets",
			"a = 1979-05-27T07:32:00Z\n", "a = 1979-05-27T00:32:00-07:00\n"},
		{"a fraction of a second", "a = 07:32:00\n", "a = 07:32:00.000001\n"},
		{"zero and minus zero", "a = 0.0\n", "a = -0.0\n"},
		{"inf and minus inf", "a = inf\n", "a = -inf\n"},
		{"inf and nan", "a = inf\n", "a = nan\n"},
		{"the order of an array", "a = [1, 2]\n", "a = [2, 1]\n"},
		{"a value", "a = 1\n", "a = 2\n"},
		{"the case of a string", "a = 'x'\n", "a = 'X'\n"},
		{"a key", "a = 1\n", "b = 1\n"},
		{"a key with a dot and a dotted key", `"a.b" = 1` + "\n", "a.b = 1\n"},
		{"an array and an array of it", "a = [[1, 2]]\n", "a = [1, 2]\n"},
		{"an empty table and none", "[a]\n", ""},
		{"an empty table and an empty array", "a = {}\n", "a = []\n"},
		{"an empty string and none", "a = ''\n", ""},
		{"one string and two", "a = ['ab']\n", "a = ['a', 'b']\n"},
		{"where a key ends", "[t]\na = 'bc'\n", "[t]\nab = 'c'\n"},
		{"a nested table and the root", "t = {a = 1}\n", "a = 1\n"},
		{"a table and an array of one table", "a = {b = 1}\n", "a = [{b = 1}]\n"},
		{"two normal forms of a letter", "a = \"é\"\n", "a = \"é\"\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.NotEqual(t, hashTOML(t, tc.a), hashTOML(t, tc.b))
		})
	}
}

// checkHashManifest is a manifest CheckHash is asked about.
const checkHashManifest = `manifest_version = '0.1'

[package]
name = 'demo'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
`

// TestManifestCheckHash pins how a manifest is checked against the
// manifest_hash of a lock: by the scheme the value names, never by the bytes
// of the file, so a lock of the scheme of an older tt is out of date even
// for the very bytes it hashed.
func TestManifestCheckHash(t *testing.T) {
	t.Parallel()

	parse := func(src string) *manifest.Manifest {
		t.Helper()

		man, _, err := manifest.ParseManifest([]byte(src))
		require.NoError(t, err)

		return man
	}

	man := parse(checkHashManifest)
	assert.Regexp(t, `^toml1-sha256:[0-9a-f]{64}$`, man.Hash())
	assert.Equal(t, hashTOML(t, checkHashManifest), man.Hash())
	require.NoError(t, man.CheckHash(man.Hash()))

	commented := parse("# The demo.\n" + checkHashManifest + "# Nothing else.\n")
	assert.Equal(t, man.Hash(), commented.Hash())
	require.NoError(t, commented.CheckHash(man.Hash()))

	edited := parse(checkHashManifest + "\n[dependencies]\nchecks = '>=3.0.0'\n")
	require.ErrorIs(t, edited.CheckHash(man.Hash()), manifest.ErrManifestChanged)

	sum := sha256.Sum256([]byte(checkHashManifest))
	digest := hex.EncodeToString(sum[:])

	outdated := man.CheckHash("sha256:" + digest)
	require.ErrorIs(t, outdated, manifest.ErrHashOutdated)
	assert.Contains(t, outdated.Error(), "tt hashes the manifest differently now")
	require.ErrorIs(t, man.CheckHash(digest), manifest.ErrHashOutdated, "no scheme")
	require.ErrorIs(t, man.CheckHash(""), manifest.ErrHashOutdated, "no value")

	unknown := man.CheckHash("toml2-sha256:" + digest)
	require.ErrorIs(t, unknown, manifest.ErrHashScheme)
	assert.Contains(t, unknown.Error(), `"toml2-sha256"`)
}
