package build_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// TestShellQuote pins how a literal is quoted: a word of safe bytes as it
// is, anything else in single quotes, a single quote escaped between them.
func TestShellQuote(t *testing.T) {
	t.Parallel()

	for literal, want := range map[string]string{
		"make":              "make",
		"./build.sh":        "./build.sh",
		"-j4":               "-j4",
		"a,b:c@d%e+f":       "a,b:c@d%e+f",
		"":                  "''",
		"V=1":               "'V=1'",
		"~/bin":             "'~/bin'",
		"#x":                "'#x'",
		"it's":              `'it'\''s'`,
		"$HOME":             "'$HOME'",
		"$(date)":           "'$(date)'",
		"`date`":            "'`date`'",
		"a b":               "'a b'",
		"one\ntwo":          "'one\ntwo'",
		`back\slash`:        `'back\slash'`,
		"''":                `''\'''\'''`,
		"echo $(date) it's": `'echo $(date) it'\''s'`,
	} {
		assert.Equal(t, want, build.ShellQuote(literal), "%q", literal)
	}
}

// TestShellQuoteIsTheWord checks with sh itself that each quoted literal is
// one word, the literal, wherever it stands: as an argument, and as the value
// of an assignment.
func TestShellQuoteIsTheWord(t *testing.T) {
	t.Parallel()

	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}

	words := []string{
		"", "plain", "V=1", "it's", "$HOME", "$(echo no)", "`echo no`", "a  b",
		"one\ntwo\n", `back\slash`, "''", "*", "~", "#x", "a;b|c&d>e<f", "\t",
	}

	var script strings.Builder

	for _, word := range words {
		script.WriteString("printf '[%s]' " + build.ShellQuote(word) + "\n")
		script.WriteString("x=" + build.ShellQuote(word) + "; printf '(%s)' \"$x\"\n")
	}

	out, err := exec.CommandContext(t.Context(), shell, "-euc", script.String()).Output()
	require.NoError(t, err)

	var want strings.Builder

	for _, word := range words {
		want.WriteString("[" + word + "](" + word + ")")
	}

	assert.Equal(t, want.String(), string(out))
}

// TestNoExportf checks that the error of a build made with tt alone wraps
// ErrNoExport and names the field.
func TestNoExportf(t *testing.T) {
	t.Parallel()

	err := build.NoExportf("static", "tt checks a %s module", "static")

	require.ErrorIs(t, err, build.ErrNoExport)

	var fieldErr *build.FieldError

	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, "static", fieldErr.Field)
	assert.Equal(t, "tt checks a static module", fieldErr.Msg)
	assert.Equal(t, "the build is made with tt alone: static: tt checks a static module",
		err.Error())
}
