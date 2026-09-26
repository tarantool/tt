package cmd

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/util"
)

// helpWidth is the column help output is wrapped at.
const helpWidth = 80

// helpTemplate is cobra's default help template with the description and
// the usage wrapped at helpWidth.
const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces | wrapHelp}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString | wrapHelp}}{{end}}`

// ExternalCommand is a top-level command that runs an external module, as
// the root help and its completion list it.
type ExternalCommand struct {
	// Name is the command's name.
	Name string
	// Help is the module's one-line description.
	Help string
}

// configureHelpCommand sets rootCmd's help command and templates, listing
// external under EXTERNAL COMMANDS in the root's usage.
func configureHelpCommand(rootCmd *cobra.Command, external []ExternalCommand) {
	cobra.AddTemplateFunc("wrapHelp", func(s string) string {
		return wrapHelp(s, helpWidth)
	})
	rootCmd.SetHelpTemplate(helpTemplate)
	// Add information about external modules into help template.
	rootCmd.SetUsageTemplate(fmt.Sprintf(usageTemplate, getExternalCommandsString(external)))

	internalHelpModule := func(cmdCtx *cmdcontext.CmdCtx, args []string) error {
		cmd, _, err := rootCmd.Find(args)
		if err != nil {
			return fmt.Errorf("failed to find the command: %w", err)
		}

		_ = cmd.Help()

		return nil
	}

	helpCmd := &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		RunE:  RunModuleFuncE(internalHelpModule),
	}

	// Add valid arguments for completion.
	for _, subCmd := range rootCmd.Commands() {
		helpCmd.ValidArgs = append(helpCmd.ValidArgs, subCmd.Name())
	}

	rootCmd.SetHelpCommand(helpCmd)
}

// getExternalCommandsString returns a pretty string of descriptions for
// external commands, sorted by name.
func getExternalCommandsString(external []ExternalCommand) string {
	var output strings.Builder

	for _, command := range sortedExternalCommands(external) {
		output.WriteString("  ")
		output.WriteString(command.Name)
		output.WriteByte('\t')
		output.WriteString(command.Help)
		output.WriteByte('\n')
	}

	if output.Len() != 0 {
		return strings.Trim(util.Bold("\nEXTERNAL COMMANDS\n")+output.String(), "\n")
	}

	return ""
}

// sortedExternalCommands returns a copy of external sorted by name.
func sortedExternalCommands(external []ExternalCommand) []ExternalCommand {
	return slices.SortedFunc(slices.Values(external), func(left, right ExternalCommand) int {
		return cmp.Compare(left.Name, right.Name)
	})
}

// wrapHelp breaks every line of s wider than width at spaces. A line with a
// gap of two or more spaces after its first word — a flag or a command with
// its description — continues under the description, a list item ("* " or
// "- ") under its text, any other line at its own indentation. A word wider
// than width is kept whole.
func wrapHelp(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = wrapHelpLine(line, width)
	}

	return strings.Join(lines, "\n")
}

func wrapHelpLine(line string, width int) string {
	if textWidth(line) <= width {
		return line
	}

	body := strings.TrimLeft(line, " \t")
	prefix := line[:len(line)-len(body)]
	indent := prefix

	if gap := strings.Index(body, "  "); gap > 0 {
		desc := strings.TrimLeft(body[gap:], " ")
		head := line[:len(line)-len(desc)]

		if desc != "" && textWidth(head) <= width/2 {
			prefix = head
			indent = strings.Repeat(" ", textWidth(head))
			body = desc
		}
	}

	if indent == prefix && (strings.HasPrefix(body, "* ") || strings.HasPrefix(body, "- ")) {
		indent = prefix + "  "
	}

	var out strings.Builder

	out.WriteString(prefix)

	col := textWidth(prefix)
	lineStart := true

	for word := range strings.FieldsSeq(body) {
		wordWidth := textWidth(word)
		if !lineStart && col+1+wordWidth > width {
			out.WriteByte('\n')
			out.WriteString(indent)

			col = textWidth(indent)
			lineStart = true
		}

		if !lineStart {
			out.WriteByte(' ')

			col++
		}

		out.WriteString(word)

		col += wordWidth

		lineStart = false
	}

	return out.String()
}

// textWidth returns the number of terminal columns text takes: one per rune,
// tabs to the next multiple of eight, ANSI escape sequences none.
func textWidth(text string) int {
	const tabWidth = 8

	col := 0
	inEscape := false

	for _, char := range text {
		switch {
		case inEscape:
			// An escape sequence ends with a byte in '@'..'~', '[' excepted.
			inEscape = char < '@' || char > '~' || char == '['
		case char == '\x1b':
			inEscape = true
		case char == '\t':
			col += tabWidth - col%tabWidth
		default:
			col++
		}
	}

	return col
}

// spell-checker:ignore rpad

var usageTemplate = util.Bold("USAGE") + `
{{- if (and .Runnable .HasAvailableInheritedFlags)}}
  {{.UseLine}}
{{end -}}

{{- if .HasAvailableSubCommands}}
  {{.CommandPath}} [flags] <command> [command flags]
{{end -}}

{{if not .HasAvailableSubCommands}}
{{- if .Runnable}}
  {{.UseLine}}
{{end -}}
{{end}}

{{- if gt (len .Aliases) 0}}` + util.Bold("\nALIASES") + `
  {{.NameAndAliases}}
{{end -}}

{{if .HasAvailableSubCommands}}` + util.Bold("\nCOMMANDS") + `
{{- range .Commands}}

{{- if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}
{{- end -}}

{{end}}
{{end -}}

{{if .HasHelpSubCommands}}` + util.Bold("\nHELP TOPICS") + `
{{- range .Commands}}

{{- if .IsAdditionalHelpTopicCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}
{{- end -}}

{{end}}
{{end -}}

{{- if not .HasParent}} %s
{{end -}}

{{- if .HasAvailableLocalFlags}}` + util.Bold("\nFLAGS") + `
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{end -}}

{{- if .HasAvailableInheritedFlags}}` + util.Bold("\nGLOBAL FLAGS") + `
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}
{{end -}}

{{- if .HasExample}}` + util.Bold("\nEXAMPLES") + `
  {{.Example}}
{{end -}}

{{- if .HasAvailableSubCommands}}
Use "{{.CommandPath}} <command> --help" for more information about a command.
{{end -}}
`
