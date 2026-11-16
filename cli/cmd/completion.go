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
	errBashCompletionGuard = errors.New(
		"failed to make bash completion check for bash-completion",
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
		Long: `Generate the completion script of tt for a shell: bash, zsh or fish.

Bash completion needs the bash-completion package, which an interactive bash
of most distributions loads once it is installed. In a shell that has not
loaded it, such as a minimal container or bash --norc, load it before the
script of tt, with . /usr/share/bash-completion/bash_completion: otherwise
Tab says the package is missing and completes nothing. In zsh, run compinit
before loading the script.`,
		ValidArgs: shellSupported,
		RunE:      internalRunE(internalCompletionCmd),
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		Example: `# Enable auto-completion in current bash shell.
  $ . <(tt completion bash)`,
	}

	return cmd
}

// requireBashCompletion makes script, cobra's bash completion of the program
// name, tell on the first Tab of a shell that has not loaded the
// bash-completion package, which defines _get_comp_words_by_ref, and complete
// nothing. cobra's fallback for a bash without _init_completion calls
// _get_comp_words_by_ref, and bash says "command not found" on every Tab.
func requireBashCompletion(script []byte, name string) ([]byte, error) {
	fallback := []byte("    COMPREPLY=()\n    _get_comp_words_by_ref \"$@\" cur prev words cword\n")

	if !bytes.Contains(script, fallback) {
		return nil, errBashCompletionGuard
	}

	guard := fmt.Sprintf(`    COMPREPLY=()
    if ! declare -F _get_comp_words_by_ref >/dev/null 2>&1; then
        if [[ -z ${__%[1]s_no_bash_completion-} ]]; then
            __%[1]s_no_bash_completion=1
            printf '\n%%s\n%%s\n' "%[1]s: completion needs the bash-completion package:" \
                "install it and load /usr/share/bash-completion/bash_completion" >&2
        fi
        return 1
    fi
    _get_comp_words_by_ref "$@" cur prev words cword
`, name)

	return bytes.Replace(script, fallback, []byte(guard), 1), nil
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

		script, err := requireBashCompletion(buf.Bytes(), rootCmd.Name())
		if err != nil {
			return err
		}

		res, err := injectRocksCompletion(shell, script)
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
