package cmd

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/rocks"
)

const (
	shellBash = "bash"
	shellZsh  = "zsh"
	shellFish = "fish"
)

var shellSupported = []string{shellBash, shellZsh, shellFish}

func listShells() string {
	return strings.Join(shellSupported, " | ")
}

// NewCompletionCmd creates a new completion command.
func NewCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "completion <SHELL_TYPE>",
		Short: "Generate autocomplete for a specified shell. " +
			fmt.Sprintf("Supported shell type: %s", listShells()),
		ValidArgs: shellSupported,
		Run:       RunModuleFunc(internalCompletionCmd),
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		Example: `
# Enable auto-completion in current bash shell.

    $ . <(tt completion bash)`,
	}

	return cmd
}

// RootShellCompletionCommands returns a list of external commands for autocomplete.
func RootShellCompletionCommands(cmd *cobra.Command, args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	var commands []string
	for name, manifest := range modulesInfo {
		commands = append(commands, fmt.Sprintf("%s\t%s", name, manifest.Help))
	}

	return commands, cobra.ShellCompDirectiveDefault
}

// injectRocksCompletion combines luarocks completions with cobra completions.
func injectRocksCompletion(shell string, completion []byte) ([]byte, error) {
	injection, err := fs.ReadFile(rocks.EmbedCompletions, "completions/"+shell+"_injection")
	if err != nil {
		return nil, err
	}

	rocks, err := fs.ReadFile(rocks.EmbedCompletions, "completions/"+shell+"_rocks")
	if err != nil {
		return nil, err
	}

	res := bytes.Buffer{}

	if shell == shellFish {
		res.Write(completion)
		res.WriteString("\n")
		res.Write(injection)
		res.Write(rocks)

	} else {
		label := []byte(`    # The user could have moved the cursor backwards on the command-line.`)
		idx := bytes.Index(completion, label)
		if idx == -1 {
			return nil, fmt.Errorf("failed to inject LuaRocks completions")
		}

		res.Write(completion[:idx])
		res.Write(injection)
		res.Write(completion[idx:])
		res.Write(rocks)
	}

	return res.Bytes(), nil
}

// internalCompletionCmd is a default (internal) completion module function.
func internalCompletionCmd(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	var buf bytes.Buffer
	switch shell := args[0]; shell {
	case shellBash:
		if err := rootCmd.GenBashCompletionV2(&buf, true); err != nil {
			return err
		}
		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}
		fmt.Print(string(res))

	case shellZsh:
		if err := rootCmd.GenZshCompletion(&buf); err != nil {
			return err
		}
		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}
		fmt.Print(string(res))

	case shellFish:
		if err := rootCmd.GenFishCompletion(&buf, true); err != nil {
			return err
		}
		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}
		fmt.Print(string(res))

	default:
		return fmt.Errorf("specified shell type is not supported. Available: %s", listShells())
	}

	return nil
}
