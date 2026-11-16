package status

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/tarantool/tt/v3/cli/running"
)

const (
	statusRowCapacity = 7
	statusColumn      = 2
	pidColumn         = 3
	modeColumn        = 4
	// unitColumn is the UNIT column, right after STATUS, when there is one:
	// the columns after it move one to the right.
	unitColumn = 3
)

var (
	printYellow = color.New(color.FgYellow).SprintFunc()
	printRed    = color.New(color.FgRed).SprintFunc()
)

// TablePrinterOption represents a configuration option for TablePrinter.
type TablePrinterOption func(*TablePrinter)

// TablePrinter implements InstanceStatusPrinter for table output.
type TablePrinter struct {
	// Options for table formatting.
	pretty  bool
	details bool
}

// WithPretty enables pretty table formatting.
func WithPretty() TablePrinterOption {
	return func(tp *TablePrinter) {
		tp.pretty = true
	}
}

// WithDetails controls detailed alert output.
func WithDetails(withDetails bool) TablePrinterOption {
	return func(tp *TablePrinter) {
		tp.details = withDetails
	}
}

// NewTablePrinter creates a new TablePrinter with the given options.
func NewTablePrinter(opts ...TablePrinterOption) *TablePrinter {
	printer := &TablePrinter{
		pretty:  false,
		details: false,
	}
	for _, opt := range opts {
		opt(printer)
	}

	return printer
}

// formatAlert formats an alert message based on its severity.
func formatAlert(alert instanceAlert) string {
	switch alert.Severity {
	case severityError:
		return printRed(alert.Message)
	case severityWarning:
		return printYellow(alert.Message)
	default:
		return alert.Message
	}
}

// hasAlerts checks if any instance has alerts.
func hasAlerts(instances map[string]*instanceStatus) bool {
	for _, instStatus := range instances {
		if len(instStatus.Alerts) > 0 {
			return true
		}
	}

	return false
}

// hasUnits checks if any instance is managed by a systemd unit.
func hasUnits(instances map[string]*instanceStatus) bool {
	for _, instStatus := range instances {
		if instStatus.Unit != "" {
			return true
		}
	}

	return false
}

// Print outputs the instance status map in table format. The UNIT column,
// the systemd unit that manages an instance, is there only when a unit
// manages some of the instances.
func (t TablePrinter) Print(instances map[string]*instanceStatus) error {
	withUnits := hasUnits(instances)

	header := table.Row{"INSTANCE", "STATUS", "PID", "MODE", "CONFIG", "BOX", "UPSTREAM"}
	shift := 0

	if withUnits {
		header = slices.Insert(header, unitColumn-1, any("UNIT"))
		shift = 1
	}

	writer := table.NewWriter()
	writer.SetOutputMirror(os.Stdout)
	writer.AppendHeader(header)

	for instName, instData := range instances {
		row := make([]any, 0, statusRowCapacity+shift)

		row = append(row, instName)
		row = append(row, instData.procStatus.FormattedStatus())

		if withUnits {
			row = append(row, instData.Unit)
		}

		if instData.PID == nil {
			writer.AppendRow(row)

			continue
		}

		row = append(row, *instData.PID)
		row = append(row, instData.Mode)
		row = append(row, instData.Config)
		row = append(row, instData.Box)
		row = append(row, instData.Upstream)
		writer.AppendRow(row)
	}

	writer.SortBy([]table.SortBy{{Name: "INSTANCE", Mode: table.Asc}})

	if t.details {
		t.printDetails(instances)
	}

	if t.pretty {
		writer.SetStyle(table.StyleRounded)
	} else {
		writer.Style().Options.DrawBorder = false
		writer.Style().Options.SeparateColumns = false
		writer.Style().Options.SeparateHeader = false
	}

	columns := []table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: statusColumn, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: pidColumn + shift, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: modeColumn + shift, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
	}

	if withUnits {
		columns = append(columns, table.ColumnConfig{
			Number: unitColumn, Align: text.AlignLeft, AlignHeader: text.AlignLeft,
		})
	}

	writer.SetColumnConfigs(columns)
	writer.Render()

	if orphans := notInConfig(instances); len(orphans) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "\nNot in the config: %s.\nRemoved from the topology "+
			"while running, they run on: tt instance stop stops them,\n"+
			"tt instance start does not start them again\n", strings.Join(orphans, ", "))
	}

	if !t.details && hasAlerts(instances) {
		msg := "\nThe status of some instances requires attention.\n" +
			"Please rerun the command with the --details flag to see " +
			"more information"

		_, _ = fmt.Fprintln(os.Stdout, msg)
	}

	if !t.details && endedUnstopped(instances) {
		_, _ = fmt.Fprintln(os.Stdout, "\nSome instances are not running and were not stopped.\n"+
			"Rerun the command with the --details flag to see how they ended")
	}

	if !t.details && restartPending(instances) {
		_, _ = fmt.Fprintln(os.Stdout, "\nSome instances do not run the active version "+
			"of their application yet: tt instance restart moves them to it.\n"+
			"Rerun the command with the --details flag to see their versions")
	}

	return nil
}

// notInConfig returns the names of the instances that run while the config
// no longer has them, sorted.
func notInConfig(instances map[string]*instanceStatus) []string {
	var names []string

	for name, instStatus := range instances {
		if instStatus.NotInConfig {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

// endedUnstopped reports whether any instance that does not run ended other
// than by a clean stop, as its watchdog recorded it.
func endedUnstopped(instances map[string]*instanceStatus) bool {
	for _, instStatus := range instances {
		last := instStatus.LastExit
		if instStatus.PID == nil && last != nil && !last.Clean() {
			return true
		}
	}

	return false
}

// restartPending reports whether any instance runs another version of its
// application than the active one.
func restartPending(instances map[string]*instanceStatus) bool {
	for _, instStatus := range instances {
		if instStatus.RestartPending {
			return true
		}
	}

	return false
}

// withDetails reports whether the table shows the details of the instances
// (detailsPrinter).
func (t TablePrinter) withDetails() bool {
	return t.details
}

// printDetails prints the details of the instances, by name: the version of
// the application of the user or system scope every running instance runs,
// with a restart pending when another one is active, the tarantool every
// Tarantool instance starts on, with the warnings of the choice, and then the
// alerts of every instance.
func (t TablePrinter) printDetails(instances map[string]*instanceStatus) {
	names := slices.Sorted(maps.Keys(instances))
	printed := false

	for _, name := range names {
		instStatus := instances[name]
		if instStatus.Version != "" {
			_, _ = fmt.Fprintf(os.Stdout, "Version of %s: %s\n", name, describeVersion(instStatus))
			printed = true
		}

		if instStatus.TCM != nil {
			printTCM(name, instStatus.TCM)

			printed = true
		}

		if printLastExit(name, instStatus) {
			printed = true
		}

		if instStatus.tarantool == "" {
			continue
		}

		_, _ = fmt.Fprintf(os.Stdout, "Tarantool for %s: %s\n", name, instStatus.tarantool)
		for _, warning := range instStatus.tarantoolWarnings {
			_, _ = fmt.Fprintf(os.Stdout, "  • %s\n", printYellow(warning))
		}

		printed = true
	}

	if printed {
		_, _ = fmt.Fprintln(os.Stdout)
	}

	for _, name := range names {
		t.printInstanceAlerts(name, instances[name])
	}
}

// detailsLogLines is how many lines of tt.log the details of an instance that
// does not run quote.
const detailsLogLines = 5

// printLastExit prints why the instance name does not run, if it has run:
// how its last process ended, and when, as its watchdog recorded it, and,
// unless it was stopped cleanly, the last lines of its tt.log, where the
// instance and its watchdog said why. It reports whether it printed anything.
func printLastExit(name string, instStatus *instanceStatus) bool {
	if instStatus.PID != nil {
		return false
	}

	last := instStatus.LastExit
	if last != nil && last.Clean() {
		_, _ = fmt.Fprintf(os.Stdout, "Last exit of %s: %s\n", name, last.Describe())

		return true
	}

	tail := ""
	if instStatus.log != "" {
		tail = running.LogTail(instStatus.log, detailsLogLines)
	}

	switch {
	case last != nil:
		_, _ = fmt.Fprintf(os.Stdout, "Last exit of %s: %s\n", name, printRed(last.Describe()))
	case tail != "":
		_, _ = fmt.Fprintf(os.Stdout, "Last exit of %s: not recorded\n", name)
	default:
		return false
	}

	if tail != "" {
		_, _ = fmt.Fprintf(os.Stdout, "  The last lines of %s:\n%s\n", instStatus.log, tail)
		_, _ = fmt.Fprintf(os.Stdout, "  tt instance logs %s prints more.\n", name)
	}

	return true
}

// printTCM prints how TCM, the instance name, is reached, and what it works
// with: the storage of its data, and what it does not see, in yellow.
func printTCM(name string, access *running.TCMAccess) {
	_, _ = fmt.Fprintf(os.Stdout, "TCM %s: %s\n", name, access.Line())
	_, _ = fmt.Fprintf(os.Stdout, "  • storage: %s\n", access.Storage)

	if access.Note != "" {
		_, _ = fmt.Fprintf(os.Stdout, "  • %s\n", printYellow(access.Note))
	}
}

// describeVersion tells the version of the application a running instance
// runs, and the version it moves to once it is restarted, if another one is
// active.
func describeVersion(instStatus *instanceStatus) string {
	if !instStatus.RestartPending {
		return instStatus.Version + ", the active version"
	}

	return fmt.Sprintf("%s, restart pending: %s is active", instStatus.Version,
		printYellow(instStatus.activeVersion))
}

// printInstanceAlerts prints alerts for a specific instance.
func (t TablePrinter) printInstanceAlerts(instanceName string, instStatus *instanceStatus) {
	if len(instStatus.Alerts) == 0 {
		return
	}

	_, _ = fmt.Fprintf(os.Stdout, "Alerts for %s:\n", instanceName)
	for _, alert := range instStatus.Alerts {
		_, _ = fmt.Fprintf(os.Stdout, "  • %s\n", formatAlert(alert))
	}

	_, _ = fmt.Fprintln(os.Stdout)
}
