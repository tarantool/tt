package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/output"

	"github.com/tarantool/tt/v3/cli/printing"
)

// bindFormatFlag declares -o/--format on a command that prints a result:
// table, json or yaml, table by default. The flag alone picks the format; a
// terminal on stdout changes nothing about it.
func bindFormatFlag(cmd *cobra.Command) *output.FormatFlag {
	return output.BindFormat(cmd.Flags(), output.FormatHuman, printing.Formats()...)
}

// emitResult writes result to stdout in the format the flag selects.
func emitResult(format *output.FormatFlag, result output.Result) error {
	printer, err := printing.NewPrinter(format.Format())
	if err != nil {
		return err
	}

	return printer.Emit(result)
}
