package connect

import (
	"strings"

	"github.com/tarantool/tt/sdk/v3/formatter"
)

// formatShortcuts are the commands that set each output format.
var formatShortcuts = map[formatter.Format]string{
	formatter.YamlFormat:   setFormatYaml,
	formatter.LuaFormat:    setFormatLua,
	formatter.TableFormat:  setFormatTable,
	formatter.TTableFormat: setFormatTTable,
}

// formats returns the output formats of the console, in the order \x
// switches through them. The help texts of tt connect and of its console
// list them from here.
func formats() []formatter.Format {
	result := make([]formatter.Format, 0, formatter.FormatsAmount)
	for format := range formatter.FormatsAmount {
		result = append(result, format)
	}

	return result
}

// formatNames returns the names of the output formats.
func formatNames() []string {
	names := make([]string, 0, formatter.FormatsAmount)
	for _, format := range formats() {
		names = append(names, format.String())
	}

	return names
}

// FormatsText lists the output formats for a help text, "yaml, lua, table
// or ttable", the default one marked if markDefault is true: "yaml
// (default), lua, table or ttable".
func FormatsText(markDefault bool) string {
	names := formatNames()

	for i, format := range formats() {
		if markDefault && format == formatter.DefaultFormat {
			names[i] += " (default)"
		}
	}

	last := len(names) - 1

	return strings.Join(names[:last], ", ") + " or " + names[last]
}

// formatShortcutsText returns the commands that set each output format, as
// the help gives them: \x[y,l,t,T].
func formatShortcutsText() string {
	letters := make([]string, 0, formatter.FormatsAmount)
	for _, format := range formats() {
		letters = append(letters, strings.TrimPrefix(formatShortcuts[format], setNextFormat))
	}

	return setNextFormat + "[" + strings.Join(letters, ",") + "]"
}

// formatShortcutCmds returns the commands that set each output format.
func formatShortcutCmds() []cmd {
	cmds := make([]cmd, 0, formatter.FormatsAmount)
	for _, format := range formats() {
		cmds = append(cmds, newNoArgsCmdDecorator(
			newBaseCmd([]string{formatShortcuts[format]}, getSetFormatFunc(format)),
		))
	}

	return cmds
}
