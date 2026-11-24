package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
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
		Short: "Show the status of instances",
		Long: `The 'status' command provides information about the status of the instances.

With no target it shows the instances of the project the command runs in;
with --scope user or --scope system, every instance of every application
installed in that scope, warning of one that cannot be read.

Columns:
- INSTANCE: The name of the instance, <APP>:<INSTANCE>.
- STATUS: The current status of the instance:
	- RUNNING: The instance is up and running.
	- NOT RUNNING: The instance is not running.
	- ERROR: The process has terminated unexpectedly.
- UNIT: The systemd unit that manages the instance, for an instance that
  tt service systemd install put under a unit. The column is shown only when
  a unit manages some of the instances.
- PID: The PID of the process the watchdog of the instance runs: tarantool,
  or the program of an external process or of TCM, the PID top, gdb, perf,
  box.info.pid and the log lines of tarantool name. It is empty while no
  process runs, as during the delay before a restart, and for a watchdog
  that an older tt started, which does not publish it: a restart of the
  instance fixes that. tt instance start, stop, kill and logrotate name an
  instance by the same PID in their messages, and by its watchdog,
  labelled so, while it is empty.
- MODE: The mode of the instance, indicating its read/write status:
	- RO: The instance is in read-only mode.
	- RW: The instance is in read-write mode.
- CONFIG: The config info status (for Tarantool 3+); -- for an instance
  that runs its script, which does not use the cluster config.
- BOX: The box info status.
- UPSTREAM: The replication upstream status.

STATUS is the state of the watchdog, which holds tt.pid in the run directory
of the instance; -o json and -o yaml give its PID as watchdog_pid. Stop an
instance with tt instance stop, not with a kill of its PID: the watchdog
takes a process that a signal ends for one that exited, and starts it again
as the restart policy of the instance says.

MODE, CONFIG, BOX and UPSTREAM are what a running Tarantool instance tells
over its console socket; an external process shows the state of its process
alone. --details prints the alerts of the instances, and the tarantool each
Tarantool instance starts on and where it comes from. For a running instance
it prints the PID of its watchdog, when its process started, how many times
the watchdog started it again and the last periodic integrity check; -o json
and -o yaml give them as started_at, restarts and last_integrity_check. For
an instance that does not run it prints how its last process ended and when,
as its watchdog recorded it, and the last lines of its tt.log unless it was
stopped cleanly; -o json and -o yaml give that record as last_exit. For
every instance it prints the last crash of its process, an exit by a
signal, with its PID, the signal and whether the kernel dumped a core, as
the watchdog recorded it: a restart and a stop later keep it, and
tt coredump list tells where the core is. -o json and -o yaml give it as
last_crash.

The status reads the cluster config as far as it can. When "*" stands for
a topology in a config storage that is out of reach, it stands for the
instances known on this host instead - named in instances.yaml, declared by
config.yaml itself, or with state under var/run or var/lib of the
application - and a warning names the storage and says that the list may be
incomplete.`,
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

// resolveStatusTargets resolves the targets of tt instance status, as
// resolveProcesses does. With no target and --scope user or system, which
// name no project, they are every instance of every application installed
// in that scope: what runs for me, or on this host. An application that
// cannot be resolved there is warned of and left out, unless it is the only
// one; with none installed, it says so and returns no instances, nil.
func resolveStatusTargets(cmdCtx *cmdcontext.CmdCtx,
	args []string,
) ([]running.InstanceCtx, error) {
	return lookupStatusTargets(context.Background(), cmdCtx, instanceFlags, args)
}

// StatusInstances resolves targets, as the command line of tt instance
// status gives them, to the instances the status would show, with the scope
// and the template of lookup, as --scope and --template give them: for
// Services.LocalInstances of the core. ctx bounds the reads of the config
// storages.
func StatusInstances(ctx context.Context, lookup sdk.AppLookup, targets []string) (
	[]running.InstanceCtx, error,
) {
	return lookupStatusTargets(ctx, &cmdCtx, lookup, targets)
}

// lookupStatusTargets is resolveStatusTargets with the scope and the
// template lookup gives rather than those of the flags of the instance
// commands.
func lookupStatusTargets(ctx context.Context, cmdCtx *cmdcontext.CmdCtx,
	lookup sdk.AppLookup, args []string,
) ([]running.InstanceCtx, error) {
	//nolint:contextcheck // The scope roots ask uname and take no context.
	opts, err := lookupOptions(cmdCtx, lookup)
	if err != nil {
		return nil, err
	}

	if len(args) != 0 || (opts.Scope != app.ScopeUser && opts.Scope != app.ScopeSystem) {
		return lookupProcesses(ctx, cmdCtx, lookup, args)
	}

	//nolint:contextcheck // As above.
	names := app.Names(opts)
	if len(names) == 0 {
		log.Infof("No applications are installed in the %s scope.", opts.Scope)

		return nil, nil
	}

	var (
		instances []running.InstanceCtx
		failures  []error
	)

	for _, name := range names {
		target := name + string(running.InstanceDelimiter) + app.AllInstances

		found, err := lookupProcesses(ctx, cmdCtx, lookup, []string{target})
		if err != nil {
			// The error names the application.
			failures = append(failures, err)

			continue
		}

		instances = append(instances, found...)
	}

	if len(failures) == len(names) {
		return nil, errors.Join(failures...)
	}

	for _, failure := range failures {
		log.Warn(failure.Error())
	}

	return instances, nil
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

	instances, err := resolveStatusTargets(cmdCtx, args)
	if err != nil {
		return err
	}

	if instances == nil {
		return nil
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
