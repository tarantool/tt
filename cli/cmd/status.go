package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/status"
)

// StatusOpts contains options for tt status.
type statusOpts struct {
	// Output format: json, yaml, table, pretty-table.
	format string
	// Option for detailed alerts output for each instance, such as warnings and errors.
	details bool
}

var opts statusOpts

// NewStatusCmd creates status command.
func NewStatusCmd() *cobra.Command {
	statusCmd := &cobra.Command{
		Use:   "status [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Status of the tarantool instance(s)",
		Long: `The 'status' command provides information about the status of Tarantool instances.

Columns:
- INSTANCE: The name of the Tarantool instance.
- STATUS: The current status of the instance:
	- RUNNING: The instance is up and running.
	- NOT RUNNING: The instance is not running.
	- ERROR: The process has terminated unexpectedly.
- PID: The watchdog process PID.
- MODE: The mode of the instance, indicating its read/write status:
	- RO: The instance is in read-only mode.
	- RW: The instance is in read-write mode.
- CONFIG: The config info status (for Tarantool 3+).
- BOX: The box info status.
- UPSTREAM: The replication upstream status.`,
		Run: RunModuleFunc(internalStatusModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractAppNames,
				running.ExtractInstanceNames)
		},
	}

	statusCmd.Flags().StringVarP(&opts.format, "format", "f", "table",
		"output format: json, yaml, table, pretty-table")
	statusCmd.Flags().BoolVarP(&opts.details, "details", "d", false, "print detailed alerts.")

	return statusCmd
}

// internalStatusModule is a default status module.
func internalStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	// Validate format option.
	validFormats := map[string]bool{
		"json":         true,
		"yaml":         true,
		"table":        true,
		"pretty-table": true,
	}
	if !validFormats[opts.format] {
		return fmt.Errorf("invalid format: %s. Valid formats are: json, yaml, table, pretty-table",
			opts.format)
	}

	var runningCtx running.RunningCtx
	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, running.ConfigLoadSkip)
	if err != nil {
		return err
	}

	var printer status.InstanceStatusPrinter
	switch opts.format {
	case "json":
		printer = status.NewJSONPrinter()
	case "yaml":
		printer = status.NewYAMLPrinter()
	case "pretty-table":
		printer = status.NewTablePrinter(status.WithPretty(), status.WithDetails(opts.details))
	case "table":
		printer = status.NewTablePrinter(status.WithDetails(opts.details))
	}

	err = status.Status(runningCtx, printer)
	return err
}
