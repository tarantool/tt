package binary

import ansicolor "github.com/mgutz/ansi"

var bold = ansicolor.ColorFunc("default+b")

// Bold makes the input string bold.
func Bold(s string) string {
	return bold(s)
}
