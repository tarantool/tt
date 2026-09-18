package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// helpWidth is the column help output is wrapped at.
const helpWidth = 80

// helpTemplate is cobra's default help template with the description and
// the usage wrapped at helpWidth.
const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces | wrapHelp}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString | wrapHelp}}{{end}}`

func configureHelpCommand(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) {
	cobra.AddTemplateFunc("wrapHelp", func(s string) string {
		return wrapHelp(s, helpWidth)
	})
	rootCmd.SetHelpTemplate(helpTemplate)
	// Add information about external modules into help template.
	rootCmd.SetUsageTemplate(fmt.Sprintf(usageTemplate, getExternalCommandsString(modulesInfo)))

	internalHelpModule := func(cmdCtx *cmdcontext.CmdCtx, args []string) error {
		cmd, _, err := rootCmd.Find(args)
		if err != nil {
			return err
		}
		_ = cmd.Help()
		return nil
	}

	helpCmd := &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Run:   RunModuleFunc(internalHelpModule),
	}

	// Add valid arguments for completion.
	for _, subCmd := range rootCmd.Commands() {
		helpCmd.ValidArgs = append(helpCmd.ValidArgs, subCmd.Name())
	}

	rootCmd.SetHelpCommand(helpCmd)
}

// getExternalCommandsString returns a pretty string
// of descriptions for external modules.
func getExternalCommandsString(modulesInfo *modules.ModulesInfo) string {
	var output strings.Builder
	for _, path := range sortExternalModules() {
		mf := (*modulesInfo)[path]
		output.WriteString("  ")
		output.WriteString(mf.Name)
		output.WriteByte('\t')
		output.WriteString(mf.Help)
		output.WriteByte('\n')
	}

	if output.Len() != 0 {
		return strings.Trim(util.Bold("\nEXTERNAL COMMANDS\n")+output.String(), "\n")
	}

	return ""
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
	for _, word := range strings.Fields(body) {
		w := textWidth(word)
		if !lineStart && col+1+w > width {
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
		col += w
		lineStart = false
	}
	return out.String()
}

// textWidth returns the number of terminal columns s takes: one per rune,
// tabs to the next multiple of eight, ANSI escape sequences none.
func textWidth(s string) int {
	col := 0
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			inEscape = !(r >= '@' && r <= '~' && r != '[')
		case r == '\x1b':
			inEscape = true
		case r == '\t':
			col += 8 - col%8
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
