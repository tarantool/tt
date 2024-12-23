package console

import (
	"fmt"

	"github.com/tarantool/tt/cli/formatter"
)

type Format struct {
	// Mode specify how to formatting result.
	Mode formatter.Format
	// Opts options for Format.
	Opts formatter.Opts
}

// Formatter interface provide common interface for console Handlers to format execution results.
type Formatter interface {
	// Format result data according fmt settings and return string for printing.
	Format(fmt Format) string
}

func (f Format) print(data any) error {
	// First ensure that data object implemented Formatter interface.
	if f_obj, ok := data.(Formatter); ok {
		fmt.Println(f_obj.Format(f))
		return nil
	}
	// Then checking is it has String method.
	if s_obj, ok := data.(fmt.Stringer); ok {
		fmt.Println(s_obj.String())
		return nil
	}
	return fmt.Errorf("can't format type=%T", data)
}

func DefaultConsoleFormat() Format {
	return Format{
		Mode: formatter.TableFormat,
		Opts: formatter.Opts{
			Graphics:       true,
			ColumnWidthMax: 0,
			TableDialect:   formatter.DefaultTableDialect,
		},
	}
}
