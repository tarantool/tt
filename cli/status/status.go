package status

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
)

// Status writes the status as a table.
func Status(runningCtx running.RunningCtx) error {
	ts := table.NewWriter()
	ts.SetOutputMirror(os.Stdout)
	ts.AppendHeader(table.Row{"INSTANCE", "STATUS", "PID", "MODE"})

	for _, run := range runningCtx.Instances {
		row := []interface{}{}
		fullInstanceName := running.GetAppInstanceName(run)
		procStatus := running.Status(&run)

		row = append(row, fullInstanceName)
		row = append(row, procStatus.ColorSprint(procStatus.Status))
		if procStatus.Code == process_utils.ProcessRunningCode {
			row = append(row, procStatus.PID)
		}

		conn, err := connector.Connect(connector.ConnectOpts{
			Network: "unix",
			Address: run.ConsoleSocket,
		})
		if err == nil {
			res, err := conn.Eval("return (type(box.cfg) == 'function') or box.info.ro",
				[]any{}, connector.RequestOpts{})
			if err == nil && len(res) != 0 {
				mode := res[0].(bool)
				mode_str := "RO"
				if !mode {
					mode_str = "RW"
				}
				row = append(row, mode_str)
			}
		}
		ts.AppendRow(row)
	}

	ts.Style().Options.DrawBorder = false
	ts.Style().Options.SeparateColumns = false
	ts.Style().Options.SeparateHeader = false
	ts.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 2, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 3, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 4, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
	})
	ts.Render()
	return nil
}
