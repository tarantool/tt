package util

import "github.com/mgutz/ansi"

var (
	bold = ansi.ColorFunc("default+b")
)

// Bold makes the input string bold.
func Bold(s string) string {
	return bold(s)
}
