package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/output"

	"github.com/tarantool/tt/v3/cli/printing"
)

// resultCommands are the commands that print a result through -o/--format.
func resultCommands() map[string]func() *cobra.Command {
	return map[string]func() *cobra.Command{
		"package deps":   newPackageDepsCmd,
		"package list":   newPackageListCmd,
		"package search": newPackageSearchCmd,
		"registry list":  newRegistryListCmd,
	}
}

// formatOf parses args into cmd's flags and returns the format -o selects.
func formatOf(t *testing.T, cmd *cobra.Command, args ...string) (output.Format, error) {
	t.Helper()

	flag := cmd.Flags().Lookup("format")
	require.NotNil(t, flag)

	format, ok := flag.Value.(*output.FormatFlag)
	require.True(t, ok, "-o is not bound with output.BindFormat")

	err := cmd.ParseFlags(args)

	return format.Format(), err
}

// TestResultCommandsDefaultToTheTable pins that the flag alone picks the
// format: without -o every result command prints the table, and the test
// binary's stdout is not a terminal, so a default that followed the
// terminal would pick something else here.
func TestResultCommandsDefaultToTheTable(t *testing.T) {
	for name, newCmd := range resultCommands() {
		t.Run(name, func(t *testing.T) {
			cmd := newCmd()

			flag := cmd.Flags().ShorthandLookup("o")
			require.NotNil(t, flag)
			assert.Equal(t, "format", flag.Name)
			assert.Equal(t, "table", flag.DefValue)
			assert.Equal(t, "output format: table, json or yaml", flag.Usage)

			format, err := formatOf(t, cmd)
			require.NoError(t, err)
			assert.Equal(t, output.FormatHuman, format)
		})
	}
}

func TestResultCommandsTakeEveryFormat(t *testing.T) {
	for name, newCmd := range resultCommands() {
		t.Run(name, func(t *testing.T) {
			for _, want := range printing.Formats() {
				format, err := formatOf(t, newCmd(), "-o", string(want))
				require.NoError(t, err)
				assert.Equal(t, want, format)
			}
		})
	}
}

func TestResultCommandsRefuseAnUnknownFormat(t *testing.T) {
	for name, newCmd := range resultCommands() {
		t.Run(name, func(t *testing.T) {
			_, err := formatOf(t, newCmd(), "-o", "xml")
			require.ErrorIs(t, err, output.ErrUnknownFormat)
		})
	}
}
