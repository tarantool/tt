package cmd

import (
	"bytes"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/rocks"
	"github.com/tarantool/tt/cli/util"
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
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		Example: `
# Enable auto-completion in current bash shell.

    $ . <(tt completion bash)`,
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

// injectRocksCompletion combines luarocks completions with cobra completions.
func injectRocksCompletion(shell string, completion []byte) ([]byte, error) {
	label := []byte(`    # The user could have moved the cursor backwards on the command-line.`)

	injection, err := fs.ReadFile(rocks.EmbedCompletions, "completions/"+shell+"_injection")
	if err != nil {
		return nil, err
	}
	rocks, err := fs.ReadFile(rocks.EmbedCompletions, "completions/"+shell+"_rocks")
	if err != nil {
		return nil, err
	}

	res := bytes.Buffer{}
	idx := bytes.Index(completion, label)
	if idx == -1 {
		return nil, fmt.Errorf("failed to inject LuaRocks completions")
	}
	res.Write(completion[:idx])
	res.Write(injection)
	res.Write(completion[idx:])
	res.Write(rocks)

	return res.Bytes(), nil
}

// internalCompletionCmd is a default (internal) completion module function.
func internalCompletionCmd(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var buf bytes.Buffer
	switch shell := args[0]; shell {
	case "bash":
		if err := rootCmd.GenBashCompletionV2(&buf, true); err != nil {
			return err
		}
		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}
		fmt.Print(string(res))
	case "zsh":
		if err := rootCmd.GenZshCompletion(&buf); err != nil {
			return err
		}
		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}
		fmt.Print(string(res))
	default:
		return fmt.Errorf("specified shell type is not is not supported. Available: bash | zsh")
	}

	return nil
}
