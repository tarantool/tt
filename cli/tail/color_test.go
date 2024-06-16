package tail_test

import (
	"testing"

	"github.com/fatih/color"
	"github.com/tarantool/tt/cli/tail"
)

func TestDefaultColorPicker(t *testing.T) {
	expectedColors := []color.Color{
		*color.New(color.FgCyan),
		*color.New(color.FgGreen),
		*color.New(color.FgMagenta),
		*color.New(color.FgYellow),
		*color.New(color.FgBlue),
	}

	colorPicker := tail.DefaultColorPicker()
	for i := 0; i < 10; i++ {
		got := colorPicker()
		got.Equals(&expectedColors[i%len(expectedColors)])
	}
}
