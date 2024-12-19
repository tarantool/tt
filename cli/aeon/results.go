package aeon

import (
	"fmt"

	"github.com/tarantool/tt/cli/aeon/pb"
	"github.com/tarantool/tt/cli/console"
	"github.com/tarantool/tt/cli/formatter"
)

// resultRow keeps values for one table row.
type resultRow []any

// resultType is a custom type to format output with console.Formatter interface.
type resultType struct {
	names []string
	rows  []resultRow
}

// resultError wraps pb.Error to implement console.Formatter interface.
type resultError struct {
	*pb.Error
}

// asYaml prepare results for formatter.MakeOutput.
func (r resultType) asYaml() string {
	yaml := "---\n"
	for _, row := range r.rows {
		mark := "-"
		for i, v := range row {
			n := r.names[i]
			yaml += fmt.Sprintf("%s %s: %v\n", mark, n, v)
			mark = " "
		}
	}
	return yaml
}

// Format produce formatted string according required console.Format settings.
func (r resultType) Format(f console.Format) (string, error) {
	if len(r.names) == 0 {
		return "", nil
	}
	output, err := formatter.MakeOutput(f.Mode, r.asYaml(), f.Opts)
	if err != nil {
		return "", err
	}
	return output, nil
}

// Format produce formatted string according required console.Format settings.
func (e *resultError) Format(_ console.Format) (string, error) {
	return fmt.Sprintf("---\nError: %s\n%q", e.Name, e.Msg), nil
}
