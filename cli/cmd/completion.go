package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/rocks"
)

var (
	errLuaRocksCompletionInjection = errors.New(
		"failed to inject LuaRocks completions",
	)
	errSpecifiedShellTypeIsNotSupportedAvailable = errors.New(
		"specified shell type is not supported. Available: ",
	)
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
			"Supported shell type: " + listShells(),
		ValidArgs: shellSupported,
		RunE:      internalRunE(internalCompletionCmd),
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		Example: `
# Enable auto-completion in current bash shell.

    $ . <(tt completion bash)`,
	}

	return cmd
}

// injectRocksCompletion combines luarocks completions with cobra completions.
func injectRocksCompletion(shell string, completion []byte) ([]byte, error) {
	injection, err := fs.ReadFile(rocks.EmbedCompletions, "completions/"+shell+"_injection")
	if err != nil {
		return nil, fmt.Errorf("failed to read rocks completion injection: %w", err)
	}

	rocks, err := fs.ReadFile(rocks.EmbedCompletions, "completions/"+shell+"_rocks")
	if err != nil {
		return nil, fmt.Errorf("failed to read rocks completion: %w", err)
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
			return nil, errLuaRocksCompletionInjection
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
		err := rootCmd.GenBashCompletionV2(&buf, true)
		if err != nil {
			return fmt.Errorf("failed to generate bash completion: %w", err)
		}

		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}

		_, _ = fmt.Fprint(os.Stdout, string(res))

	case shellZsh:
		err := rootCmd.GenZshCompletion(&buf)
		if err != nil {
			return fmt.Errorf("failed to generate zsh completion: %w", err)
		}

		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}

		_, _ = fmt.Fprint(os.Stdout, string(res))

	case shellFish:
		err := rootCmd.GenFishCompletion(&buf, true)
		if err != nil {
			return fmt.Errorf("failed to generate fish completion: %w", err)
		}

		res, err := injectRocksCompletion(shell, buf.Bytes())
		if err != nil {
			return err
		}

		_, _ = fmt.Fprint(os.Stdout, string(res))

	default:
		return fmt.Errorf("%w%s", errSpecifiedShellTypeIsNotSupportedAvailable, listShells())
	}

	return nil
}
