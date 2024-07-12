package tail

import "github.com/fatih/color"

// ColorPicker returns a color.
type ColorPicker func() color.Color

// DefaultColorPicker create a color picker to get a color from a default colors set.
func DefaultColorPicker() ColorPicker {
	var colorTable = []color.Color{
		*color.New(color.FgHiBlue),
		*color.New(color.FgHiCyan),
		*color.New(color.FgHiMagenta),
		*color.New(color.FgBlue),
		*color.New(color.FgCyan),
		*color.New(color.FgMagenta),
	}

	i := 0
	return func() color.Color {
		color := colorTable[i]
		i = (i + 1) % len(colorTable)
		return color
	}
}
