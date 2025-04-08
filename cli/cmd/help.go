package cmd

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

func configureHelpCommand(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) error {
	// Add information about external modules into help template.
	cobra.AddTemplateFunc("title", util.Bold)
	cobra.AddTemplateFunc("split", strings.Split)
	rootCmd.SetUsageTemplate(usageTemplate)

	internalHelpModule := func(cmdCtx *cmdcontext.CmdCtx, args []string) error {
		// fmt.Printf("internalHelpModule: cmdCtx.CommandName=%s\n", cmdCtx.CommandName)
		// fmt.Printf("internalHelpModule: args: %v\n", args)
		if len(args) == 0 {
			rootCmd.Help()
			return nil
		}

		cmd, _, err := rootCmd.Find(args)
		// fmt.Printf("internalHelpModule: cmd.Name=%s\n", cmd.Name())
		// fmt.Printf("internalHelpModule: cmd.CommandPath=%s\n", cmd.CommandPath())
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
	for name := range *modulesInfo {
		helpCmd.ValidArgs = append(helpCmd.ValidArgs, name)
	}

	rootCmd.SetHelpCommand(helpCmd)
	return nil
}

var (
	usageTemplate = `{{title "USAGE"}}
{{- if (and .Runnable .HasAvailableInheritedFlags)}}
  {{.UseLine}}
{{end -}}

{{- if .HasAvailableSubCommands}}
  {{.CommandPath}} [flags] <command> [command flags]
{{- else if .Runnable}}
  {{.UseLine}}
{{- end}}
{{- if gt (len .Aliases) 0}}

{{title "ALIASES"}}
  {{.NameAndAliases}}
{{- end}}
{{- if .HasAvailableSubCommands}}

{{title "COMMANDS"}}
{{- range .Commands}}
{{- if or .IsAvailableCommand (eq .Name "help")}}
{{- if eq .GroupID ""}}
  {{rpad .Name .NamePadding }} {{.Short}}
{{- else if ne .GroupID "External"}}
  {{rpad .Name .NamePadding }} {{index (split .Short "|") 0}}
{{- end}}
{{- end}}
{{- end}}
{{- end}}
{{- if gt (len .Groups) 0}}

{{title "EXTERNAL COMMANDS"}}
{{- range .Commands}}
{{- if or .IsAvailableCommand (eq .Name "help")}}
{{- if eq .GroupID "External"}}
  {{rpad .Name .NamePadding }} {{.Short}}
{{- else if ne .GroupID ""}}
  {{rpad .Name .NamePadding }} {{index (split .Short "|") 1}}
{{- end}}
{{- end}}
{{- end}}
{{- end}}
{{- if .HasAvailableLocalFlags}}

{{title "FLAGS"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{- end}}
{{- if .HasAvailableInheritedFlags}}

{{title "GLOBAL FLAGS"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}
{{- end}}
{{- if .HasExample}}

{{title "EXAMPLES"}}
  {{.Example}}
{{- end}}
{{- if .HasAvailableSubCommands}}

Use "{{.CommandPath}} <command> --help" for more information about a command.
{{- end}}
`
)
