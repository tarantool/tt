package output_test

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/output"
)

const formatYAML output.Format = "yaml"

// newFlagSet returns a flag set with the format flag bound the way tt's
// result-printing commands bind it.
func newFlagSet(t *testing.T) (*pflag.FlagSet, *output.FormatFlag) {
	t.Helper()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flag := output.BindFormat(fs, output.FormatHuman,
		output.FormatHuman, output.FormatJSON, formatYAML)

	return fs, flag
}

func TestBindFormatDefaultIsTheFallback(t *testing.T) {
	t.Parallel()

	fs, flag := newFlagSet(t)
	require.NoError(t, fs.Parse(nil))

	assert.Equal(t, output.FormatHuman, flag.Format())
}

func TestBindFormatTakesEveryAcceptedValue(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"-o", "json"},
		{"--format", "json"},
		{"--format=json"},
		{"-ojson"},
	} {
		fs, flag := newFlagSet(t)
		require.NoError(t, fs.Parse(args), args)
		assert.Equal(t, output.FormatJSON, flag.Format(), args)
	}

	fs, flag := newFlagSet(t)
	require.NoError(t, fs.Parse([]string{"-o", "yaml"}))
	assert.Equal(t, formatYAML, flag.Format())

	fs, flag = newFlagSet(t)
	require.NoError(t, fs.Parse([]string{"-o", "table"}))
	assert.Equal(t, output.FormatHuman, flag.Format())
}

func TestBindFormatEmptyValueIsTheFallback(t *testing.T) {
	t.Parallel()

	fs, flag := newFlagSet(t)
	require.NoError(t, fs.Parse([]string{"--format="}))

	assert.Equal(t, output.FormatHuman, flag.Format())
}

func TestBindFormatRefusesAnUnknownValue(t *testing.T) {
	t.Parallel()

	fs, flag := newFlagSet(t)
	err := fs.Parse([]string{"-o", "xml"})

	require.ErrorIs(t, err, output.ErrUnknownFormat)
	require.ErrorContains(t, err, `unknown output format "xml" (want table, json or yaml)`)
	assert.Equal(t, output.FormatHuman, flag.Format())
}

func TestBindFormatRefusesAFormatTheCommandDoesNotAccept(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	output.BindFormat(fs, output.FormatHuman, output.FormatHuman, output.FormatJSON)

	err := fs.Parse([]string{"-o", "yaml"})
	require.ErrorIs(t, err, output.ErrUnknownFormat)
	assert.ErrorContains(t, err, "(want table or json)")
}

func TestBindFormatHelpNamesTheValuesAndTheDefault(t *testing.T) {
	t.Parallel()

	fs, _ := newFlagSet(t)
	usage := fs.FlagUsages()

	assert.Contains(t, usage, "-o, --format string")
	assert.Contains(t, usage, `output format: table, json or yaml (default "table")`)
}

func TestBindFormatHelpWithAMachineDefault(t *testing.T) {
	t.Parallel()

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	output.BindFormat(fs, output.FormatJSON, output.FormatJSON)

	assert.Contains(t, fs.FlagUsages(), `output format: json (default "json")`)
}

func TestBindFormatPanicsOnACommandMistake(t *testing.T) {
	t.Parallel()

	newFS := func() *pflag.FlagSet { return pflag.NewFlagSet("test", pflag.ContinueOnError) }

	assert.PanicsWithValue(t, "output.BindFormat: no formats", func() {
		output.BindFormat(newFS(), output.FormatHuman)
	})
	assert.PanicsWithValue(t, `output.BindFormat: format "json" given twice`, func() {
		output.BindFormat(newFS(), output.FormatJSON, output.FormatJSON, output.FormatJSON)
	})
	assert.PanicsWithValue(t, `output.BindFormat: default "table" is not among json or yaml`,
		func() {
			output.BindFormat(newFS(), output.FormatHuman, output.FormatJSON, formatYAML)
		})
}
