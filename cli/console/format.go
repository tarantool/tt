package console

import "github.com/tarantool/tt/cli/formatter"

type Format struct {
	// Mode specify how to formatting result.
	Mode formatter.Format
	// Opts options for Format.
	Opts formatter.Opts
}

func (f Format) print(HandlerResult) error {
	// TODO: implement formatting and print results.
	return nil
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
