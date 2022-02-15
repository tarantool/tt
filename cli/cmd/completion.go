package cmd

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
)

// NewCompletionCmd creates a new completion command.
func NewCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion <shell type>",
		Short:     "Generate autocomplete for a specified shell",
		ValidArgs: []string{"bash", "zsh"},
		Run: func(cmd *cobra.Command, args []string) {
			args = modules.GetDefaultCmdArgs(cmd.Name())
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalCompletionCmd, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return cmd
}

// RootShellCompletionCommands returns a list of external commands for autocomplete.
func RootShellCompletionCommands(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var commands []string
	for name, info := range modulesInfo {
		if !info.IsInternal {
			description, err := modules.GetExternalModuleDescription(info.ExternalPath)
			if err != nil {
				description = "Failed to get description"
			}

			commands = append(commands, fmt.Sprintf("%s\t%s", name, description))
		}
	}

	return commands, cobra.ShellCompDirectiveDefault
}

// internalCompletionCmd is a default (internal) completion module function.
func internalCompletionCmd(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	switch shell := args[0]; shell {
	case "bash":
		if err := rootCmd.GenBashCompletion(os.Stdout); err != nil {
			return err
		}
	case "zsh":
		if err := rootCmd.GenZshCompletion(os.Stdout); err != nil {
			return err
		}
	}

	return nil
}
