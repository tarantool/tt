package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/status"
)

var (
	errInvalidFormatValidFormatsAreJSONYAMLTablePrettyTable = errors.New("invalid format: ")
	errNegativeInstanceTimeout                              = errors.New(
		"instance timeout must not be negative",
	)
)

// The output formats of tt instance status.
const (
	statusFormatJSON        = "json"
	statusFormatYAML        = "yaml"
	statusFormatTable       = "table"
	statusFormatPrettyTable = "pretty-table"
)

// statusOpts contains options for tt instance status.
type statusOpts struct {
	// Output format: json, yaml, table, pretty-table.
	format string
	// Option for detailed alerts output for each instance, such as warnings and errors.
	details bool
	// instanceTimeout bounds status collection for each instance; zero disables it.
	instanceTimeout time.Duration
}

var opts statusOpts

// NewStatusCmd creates the status command of tt instance.
func NewStatusCmd() *cobra.Command {
	statusCmd := &cobra.Command{
		Use:   "status " + instanceTargetsUsage,
		Short: "Status of instance(s)",
		Long: `The 'status' command provides information about the status of the instances.

Columns:
- INSTANCE: The name of the instance, <APP>:<INSTANCE>.
- STATUS: The current status of the instance:
	- RUNNING: The instance is up and running.
	- NOT RUNNING: The instance is not running.
	- ERROR: The process has terminated unexpectedly.
- UNIT: The systemd unit that manages the instance, for an instance that
  tt service systemd install put under a unit. The column is shown only when
  a unit manages some of the instances.
- PID: The watchdog process PID.
- MODE: The mode of the instance, indicating its read/write status:
	- RO: The instance is in read-only mode.
	- RW: The instance is in read-write mode.
- CONFIG: The config info status (for Tarantool 3+).
- BOX: The box info status.
- UPSTREAM: The replication upstream status.

MODE, CONFIG, BOX and UPSTREAM are what a running Tarantool instance tells
over its console socket; an external process shows the state of its process
alone. --details prints the alerts of the instances, and the tarantool each
Tarantool instance starts on and where it comes from.`,
		RunE: internalRunE(internalStatusModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Any)
		},
	}

	addInstanceFlags(statusCmd)

	// -o, as every command that prints a result has it: -f is --force.
	statusCmd.Flags().StringVarP(&opts.format, "format", "o", statusFormatTable,
		"output format: json, yaml, table, pretty-table")
	statusCmd.Flags().BoolVarP(&opts.details, "details", "d", false,
		"print detailed alerts and the tarantool the instances start on")

	statusCmd.Flags().DurationVar(&opts.instanceTimeout, "instance-timeout",
		status.DefaultInstanceTimeout,
		"timeout for collecting a single instance's status; 0 disables the timeout")

	return statusCmd
}

// internalStatusModule is a default status module.
func internalStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if opts.instanceTimeout < 0 {
		return errNegativeInstanceTimeout
	}

	// Validate format option.
	validFormats := map[string]bool{
		statusFormatJSON:        true,
		statusFormatYAML:        true,
		statusFormatTable:       true,
		statusFormatPrettyTable: true,
	}
	if !validFormats[opts.format] {
		return fmt.Errorf("%w%s. Valid formats are: json, yaml, table, pretty-table",
			errInvalidFormatValidFormatsAreJSONYAMLTablePrettyTable, opts.format)
	}

	instances, err := resolveProcesses(cmdCtx, args)
	if err != nil {
		return err
	}

	var printer status.InstanceStatusPrinter

	switch opts.format {
	case statusFormatJSON:
		printer = status.NewJSONPrinter()
	case statusFormatYAML:
		printer = status.NewYAMLPrinter()
	case statusFormatPrettyTable:
		printer = status.NewTablePrinter(status.WithPretty(), status.WithDetails(opts.details))
	case statusFormatTable:
		printer = status.NewTablePrinter(status.WithDetails(opts.details))
	}

	err = status.Status(running.RunningCtx{Instances: instances}, printer, opts.instanceTimeout)
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	return nil
}
