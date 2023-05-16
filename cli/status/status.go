package status

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
)

const (
	// colorBytesNumber is the number of bytes added to the colorized string.
	colorBytesNumber = 9

	// padding is the padding between two columns of the table.
	padding = 5
)

var header = []string{"INSTANCE", "STATUS", "PID"}

// Status writes the status as a table.
func Status(runningCtx running.RunningCtx) error {
	instColWidth := len(header[0])
	sb := strings.Builder{}
	tw := tabwriter.NewWriter(&sb, 0, 1, padding, ' ', 0)

	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, run := range runningCtx.Instances {
		fullInstanceName := running.GetAppInstanceName(run)
		procStatus := running.Status(&run)
		if len(fullInstanceName) > instColWidth {
			instColWidth = len(fullInstanceName)
		}

		fmt.Fprintf(tw, "%s\t%s\t", fullInstanceName,
			procStatus.ColorSprint(procStatus.Status))
		if procStatus.Code == process_utils.ProcessRunningCode {
			fmt.Fprintf(tw, "%d", procStatus.PID)
		}
		fmt.Fprintf(tw, "\n")
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	rawOutput := sb.String()
	rawHeader, rest, _ := strings.Cut(rawOutput, "\n")

	// Calculating the position of the `status` end in the header.
	statusOffset := instColWidth + padding + len(header[1])
	fmt.Print(rawHeader[:statusOffset])

	var toSkip int
	if len(runningCtx.Instances) > 0 && !color.NoColor {
		// We need to skip the spaces that appear
		// as a result of using color bytes, if any.
		toSkip = colorBytesNumber
	}
	fmt.Println(rawHeader[statusOffset+toSkip:])
	fmt.Print(rest)
	return nil
}
