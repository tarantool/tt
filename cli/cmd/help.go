package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

func configureHelpCommand(rootCmd *cobra.Command, modulesInfo *modules.ModulesInfo) error {
	// Add information about external modules into help template.
	rootCmd.SetUsageTemplate(fmt.Sprintf(usageTemplate, getExternalCommandsString(modulesInfo)))

	internalHelpModule := func(cmdCtx *cmdcontext.CmdCtx, args []string) error {
		fmt.Printf("internalHelpModule: cmdCtx.CommandName=%s\n", cmdCtx.CommandName)
		fmt.Printf("internalHelpModule: args: %v\n", args)
		if len(args) == 0 {
			rootCmd.Help()
			return nil
		}

		cmd, _, err := rootCmd.Find(args)
		fmt.Printf("internalHelpModule: cmd.Name=%s\n", cmd.Name())
		fmt.Printf("internalHelpModule: cmd.CommandPath=%s\n", cmd.CommandPath())
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

// getExternalCommandString returns a pretty string
// of descriptions for external modules.
func getExternalCommandsString(modulesInfo *modules.ModulesInfo) string {
	str := ""
	for path, manifest := range *modulesInfo {
		helpMsg, err := modules.GetExternalModuleDescription(manifest)
		if err != nil {
			helpMsg = "description is absent"
		}

		name := strings.Split(path, " ")[1]
		str = fmt.Sprintf("%s  %s\t%s\n", str, name, helpMsg)
	}

	if str != "" {
		str = util.Bold("\nEXTERNAL COMMANDS\n") + str
		return strings.Trim(str, "\n")
	}

	return ""
}

var (
	usageTemplate = util.Bold("USAGE") + `
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

{{- if not .HasAvailableInheritedFlags}} %s
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
)
