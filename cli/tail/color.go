package tail

import "github.com/fatih/color"

// ColorPicker returns a color.
type ColorPicker func() color.Color

// DefaultColorPicker create a color picker to get a color from a default colors set.
func DefaultColorPicker() ColorPicker {
	var colorTable = []color.Color{
		*color.New(color.FgCyan),
		*color.New(color.FgGreen),
		*color.New(color.FgMagenta),
		*color.New(color.FgYellow),
		*color.New(color.FgBlue),
	}

	i := 0
	return func() color.Color {
		color := colorTable[i]
		i = (i + 1) % len(colorTable)
		return color
	}
}
