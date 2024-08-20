package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// DefaultHelpFunc is a type of the standard built-in
// cobra implementation of the help function.
type DefaultHelpFunc func(*cobra.Command, []string)

// configureHelpCommand configures our own help module
// so that it can be external as well.
// If the help is called for an external module
// (for example `tt help version`), we try to get the help from it.
func configureHelpCommand(cmdCtx *cmdcontext.CmdCtx, rootCmd *cobra.Command) error {
	// Add information about external modules into help template.
	rootCmd.SetUsageTemplate(fmt.Sprintf(usageTemplate, getExternalCommandsString(&modulesInfo)))
	defaultHelp := rootCmd.HelpFunc()

	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		cmdCtx.CommandName = cmd.Name()
		if len(os.Args) == 1 || util.Find(args, "-h") != -1 || util.Find(args, "--help") != -1 {
			defaultHelp(cmd, nil)
			return
		}

		args = modules.GetDefaultCmdArgs("help")
		err := modules.RunCmd(cmdCtx, "tt help", &modulesInfo,
			getInternalHelpFunc(cmd, defaultHelp), args)
		if err != nil {
			log.Fatalf(err.Error())
		}
	})

	// Add valid arguments for completion.
	helpCmd := util.GetHelpCommand(rootCmd)
	for name := range modulesInfo {
		helpCmd.ValidArgs = append(helpCmd.ValidArgs, name)
	}

	return nil
}

// getInternalHelpFunc returns a internal implementation of help module.
func getInternalHelpFunc(cmd *cobra.Command, help DefaultHelpFunc) modules.InternalFunc {
	return func(cmdCtx *cmdcontext.CmdCtx, args []string) error {
		switch module := modulesInfo[cmd.CommandPath()]; {
		// Cases when we have to run the "default" help:
		// - `tt help` and no external help module.
		// 	It looks strange: if we type the command `tt help`,
		// 	the call to cmd.Name() returns `tt` and I don’t know
		// 	what is the reason. If there is an external help module,
		// 	then we also cannot be here (see the code below) and it
		// 	is enough to check cmd.Name() == "tt".
		// - `tt help -I`
		// - `tt --help`, `tt -h` or `tt` (look code above).
		case cmd.Name() == "tt", module.IsInternal:
			help(cmd, nil)
		// We make a call to the external module (if it exists) with the `--help` flag.
		default:
			helpMsg, err := modules.GetExternalModuleHelp(module.ExternalPath)
			if err != nil {
				return err
			}

			cmd.Print(helpMsg)
		}

		return nil
	}
}

// getExternalCommandString returns a pretty string
// of descriptions for external modules.
func getExternalCommandsString(modulesInfo *modules.ModulesInfo) string {
	str := ""
	for path, info := range *modulesInfo {
		if !info.IsInternal {
			helpMsg, err := modules.GetExternalModuleDescription(info.ExternalPath)
			if err != nil {
				helpMsg = "description is absent"
			}

			name := strings.Split(path, " ")[1]
			str = fmt.Sprintf("%s  %s\t%s\n", str, name, helpMsg)
		}
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
