package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
)

// NewCompletionCmd creates a new completion command.
func NewCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "completion <SHELL_TYPE>",
		Short:     "Generate autocomplete for a specified shell. Supported shell type: bash | zsh",
		ValidArgs: []string{"bash", "zsh"},
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			args = modules.GetDefaultCmdArgs(cmd.Name())
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalCompletionCmd, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	}

	return cmd
}

// RootShellCompletionCommands returns a list of external commands for autocomplete.
func RootShellCompletionCommands(cmd *cobra.Command, args []string,
	toComplete string) ([]string, cobra.ShellCompDirective) {
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
		if err := rootCmd.GenBashCompletionV2(os.Stdout, true); err != nil {
			return err
		}
	case "zsh":
		if err := rootCmd.GenZshCompletion(os.Stdout); err != nil {
			return err
		}
	default:
		return fmt.Errorf("specified shell type is not is not supported. Available: bash | zsh")
	}

	return nil
}
