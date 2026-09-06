package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

func configureHelpCommand(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) {
	// Add information about external modules into help template.
	rootCmd.SetUsageTemplate(fmt.Sprintf(usageTemplate, getExternalCommandsString(modulesInfo)))

	internalHelpModule := func(cmdCtx *cmdcontext.CmdCtx, args []string) error {
		cmd, _, err := rootCmd.Find(args)
		if err != nil {
			return err
		}
		cmd.Help()
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
