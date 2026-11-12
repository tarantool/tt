package cmd

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/util"
	"golang.org/x/term"
)

// helpWidth is the column help output is wrapped at.
const helpWidth = 80

// helpTemplate is cobra's default help template with the description and
// the usage wrapped at helpWidth.
const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces | wrapHelp}}

{{end}}{{if or .Runnable .HasSubCommands}}{{usage . | wrapHelp}}{{end}}`

// helpColors holds, for a command whose help is being written, whether its
// headings are bold (colorsHelp of the writer of the help). The usage goes
// through UsageString (usageOf), which points the writers of the command at
// a buffer while it renders, so helpHeading cannot tell the writer itself.
//
//nolint:gochecknoglobals // The state of a help being written, by command.
var helpColors sync.Map

// usageOf returns the usage of cmd, its headings bold when the help of cmd
// goes to a terminal that takes colour.
func usageOf(cmd *cobra.Command) string {
	helpColors.Store(cmd, colorsHelp(isTerminal(cmd.OutOrStdout()), os.LookupEnv))
	defer helpColors.Delete(cmd)

	return cmd.UsageString()
}

// ExternalCommand is a top-level command that runs an external module, as
// the root help lists it under EXTERNAL COMMANDS.
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
	cobra.AddTemplateFunc("heading", helpHeading)
	cobra.AddTemplateFunc("usage", usageOf)
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
		RunE:  internalRunE(internalHelpModule),
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
		// A part of the usage template, after the space that precedes it.
		return "\n{{heading . \"EXTERNAL COMMANDS\"}}\n" +
			strings.TrimSuffix(output.String(), "\n")
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

// The usage line of a runnable command is printed once. A command with
// subcommands adds the form that runs one, and shows its own usage line beside
// it only when it inherits flags: the root shows the subcommand form alone.
//
// The headings go through heading (helpHeading), which makes them bold only
// on a terminal.
const usageTemplate = `{{heading . "USAGE"}}
{{- if (and .Runnable (or .HasAvailableInheritedFlags (not .HasAvailableSubCommands)))}}
  {{.UseLine}}
{{end -}}

{{- if .HasAvailableSubCommands}}
  {{.CommandPath}} [flags] <command> [command flags]
{{end -}}

{{- if gt (len .Aliases) 0}}
{{heading . "ALIASES"}}
  {{.NameAndAliases}}
{{end -}}

{{if .HasAvailableSubCommands}}
{{heading . "COMMANDS"}}
{{- range .Commands}}

{{- if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}
{{- end -}}

{{end}}
{{end -}}

{{if .HasHelpSubCommands}}
{{heading . "HELP TOPICS"}}
{{- range .Commands}}

{{- if .IsAdditionalHelpTopicCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}
{{- end -}}

{{end}}
{{end -}}

{{- if not .HasParent}} %s
{{end -}}

{{- if .HasAvailableLocalFlags}}
{{heading . "FLAGS"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{end -}}

{{- if .HasAvailableInheritedFlags}}
{{heading . "GLOBAL FLAGS"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}
{{end -}}

{{- if .HasExample}}
{{heading . "EXAMPLES"}}
  {{.Example}}
{{end -}}

{{- if .HasAvailableSubCommands}}
Use "{{.CommandPath}} <command> --help" for more information about a command.
{{end -}}
`

// helpHeading returns title, the heading of a part of the help of cmd: bold
// when cmd writes its help to a terminal that takes colour, as it is when
// the help is piped or written to a file. NO_COLOR set to a value
// (https://no-color.org) and TERM=dumb turn the colour off, as they do for
// the log. The usage alone, which cobra writes to the error stream, is told
// by that stream.
func helpHeading(cmd *cobra.Command, title string) string {
	color, ok := helpColors.Load(cmd)
	if !ok {
		color = colorsHelp(isTerminal(cmd.OutOrStderr()), os.LookupEnv)
	}

	if bold, _ := color.(bool); !bold {
		return title
	}

	return util.Bold(title)
}

// colorsHelp reports whether help written to a terminal, or not, is
// coloured: on a terminal, unless NO_COLOR is set to a value or TERM is
// dumb, as lookupEnv reads them.
func colorsHelp(terminal bool, lookupEnv func(string) (string, bool)) bool {
	if !terminal {
		return false
	}

	if value, set := lookupEnv("NO_COLOR"); set && value != "" {
		return false
	}

	terminalType, set := lookupEnv("TERM")

	return !set || terminalType != "dumb"
}

// isTerminal reports whether out is an *os.File open on a terminal.
func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)

	return ok && term.IsTerminal(int(file.Fd()))
}
