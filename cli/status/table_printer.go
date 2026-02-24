package status

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

var (
	printYellow = color.New(color.FgYellow).SprintFunc()
	printRed    = color.New(color.FgRed).SprintFunc()
)

// TablePrinterOption represents a configuration option for TablePrinter.
type TablePrinterOption func(*TablePrinter)

// TablePrinter implements InstanceStatusPrinter for table output.
type TablePrinter struct {
	// Options for table formatting
	pretty  bool
	details bool
}

// WithPretty enables pretty table formatting.
func WithPretty() TablePrinterOption {
	return func(tp *TablePrinter) {
		tp.pretty = true
	}
}

// WithDetails enables detailed alert output.
func WithDetails(withDetails bool) TablePrinterOption {
	return func(tp *TablePrinter) {
		tp.details = withDetails
	}
}

// NewTablePrinter creates a new TablePrinter with the given options.
func NewTablePrinter(opts ...TablePrinterOption) *TablePrinter {
	tp := &TablePrinter{
		pretty:  false,
		details: false,
	}
	for _, opt := range opts {
		opt(tp)
	}
	return tp
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

// printInstanceAlerts prints alerts for a specific instance.
func (t TablePrinter) printInstanceAlerts(instanceName string, instStatus *instanceStatus) {
	if len(instStatus.Alerts) == 0 {
		return
	}
	fmt.Printf("Alerts for %s:\n", instanceName)
	for _, alert := range instStatus.Alerts {
		fmt.Printf("  • %s\n", formatAlert(alert))
	}
	fmt.Println()
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

// Print outputs the instance status map in table format.
func (t TablePrinter) Print(instances map[string]*instanceStatus) error {
	ts := table.NewWriter()
	ts.SetOutputMirror(os.Stdout)
	ts.AppendHeader(
		table.Row{"INSTANCE", "STATUS", "PID", "MODE", "CONFIG", "BOX", "UPSTREAM"})

	for instName, instData := range instances {
		row := []any{}
		row = append(row, instName)
		row = append(row, instData.procStatus.FormattedStatus())
		if instData.PID == nil {
			ts.AppendRow(row)
			continue
		}
		row = append(row, *instData.PID)
		row = append(row, instData.Mode)
		row = append(row, instData.Config)
		row = append(row, instData.Box)
		row = append(row, instData.Upstream)
		ts.AppendRow(row)
	}
	ts.SortBy([]table.SortBy{{Name: "INSTANCE", Mode: table.Asc}})

	if t.details {
		for instanceName, instStatus := range instances {
			t.printInstanceAlerts(instanceName, instStatus)
		}
	}
	if t.pretty {
		ts.SetStyle(table.StyleRounded)
	} else {
		ts.Style().Options.DrawBorder = false
		ts.Style().Options.SeparateColumns = false
		ts.Style().Options.SeparateHeader = false
	}
	ts.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 2, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 3, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 4, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
	})
	ts.Render()

	if !t.details && hasAlerts(instances) {
		msg := "\nThe status of some instances requires attention.\n" +
			"Please rerun the command with the --details flag to see " +
			"more information"
		fmt.Println(msg)
	}

	return nil
}
