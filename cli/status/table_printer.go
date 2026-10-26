package status

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
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

	if !t.details && hasAlerts(instances) {
		msg := "\nThe status of some instances requires attention.\n" +
			"Please rerun the command with the --details flag to see " +
			"more information"

		_, _ = fmt.Fprintln(os.Stdout, msg)
	}

	return nil
}

// withDetails reports whether the table shows the details of the instances
// (detailsPrinter).
func (t TablePrinter) withDetails() bool {
	return t.details
}

// printDetails prints the details of the instances, by name: the tarantool
// every Tarantool instance starts on, with the warnings of the choice, and
// then the alerts of every instance.
func (t TablePrinter) printDetails(instances map[string]*instanceStatus) {
	names := slices.Sorted(maps.Keys(instances))
	printed := false

	for _, name := range names {
		instStatus := instances[name]
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
