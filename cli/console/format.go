package console

import (
	"fmt"

	"github.com/tarantool/tt/cli/formatter"
)

// Format aggregate formatter options.
type Format struct {
	// Mode specify how to formatting result.
	Mode formatter.Format
	// Opts options for Format.
	Opts formatter.Opts
}

// FormatAsTable return Format options for formatting outputs as table.
func FormatAsTable() Format {
	return Format{
		Mode: formatter.TableFormat,
		Opts: formatter.Opts{
			Graphics:       true,
			ColumnWidthMax: 0,
			TableDialect:   formatter.DefaultTableDialect,
		},
	}
}

func (f Format) Sprint(data any) (string, error) {
	if fo, ok := data.(Formatter); ok {
		// First ensure that data object implemented `Formatter` interface.
		return fo.Format(f)
	} else if so, ok := data.(fmt.Stringer); ok {
		// Then checking is it has `String` method.
		return so.String(), nil
	} else if s, ok := data.(string); ok {
		// Then checking is it `string` type.
		return s, nil
	} else if eo, ok := data.(error); ok {
		// Then checking is it has `Error` method.
		return fmt.Sprintf("Error: %s", eo.Error()), nil
	}
	return "", fmt.Errorf("can't format type=%T", data)
}
