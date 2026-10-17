package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

const projectEnvironmentShellTestScript = `
set -eu
unset TT_PROJECT_DIR TT_USER_OLD_PATH
unset TT_USER_OLD_TARANTOOL_PREFIX TT_USER_OLD_TARANTOOL_PREFIX_SET
case "$3" in
    unset) unset TT_CLI_TARANTOOL_PREFIX ;;
    empty) export TT_CLI_TARANTOOL_PREFIX='' ;;
    custom) export TT_CLI_TARANTOOL_PREFIX='/previous prefix' ;;
esac
old_path=$PATH
old_prefix_set=${TT_CLI_TARANTOOL_PREFIX+x}
old_prefix=${TT_CLI_TARANTOOL_PREFIX-}

. "$2"
[ "$PATH" = "$old_path" ]
[ "${TT_CLI_TARANTOOL_PREFIX+x}" = "$old_prefix_set" ]

. "$1"
[ "$TT_PROJECT_DIR" = "$4" ]
[ "$TT_USER_OLD_PATH" = "$old_path" ]
[ "$PATH" = "$4/.tarantool/bin:$old_path" ]
[ "$TT_CLI_TARANTOOL_PREFIX" = "$4/.tarantool" ]
"$5" -c '[ "$TT_PROJECT_DIR" = "$1" ] &&
    [ "$TT_CLI_TARANTOOL_PREFIX" = "$1/.tarantool" ]' project-environment "$4"

. "$1"
[ "$TT_USER_OLD_PATH" = "$old_path" ]
[ "$PATH" = "$4/.tarantool/bin:$old_path" ]

. "$2"
[ "$PATH" = "$old_path" ]
[ "${TT_CLI_TARANTOOL_PREFIX+x}" = "$old_prefix_set" ]
[ "${TT_CLI_TARANTOOL_PREFIX-}" = "$old_prefix" ]
[ "${TT_PROJECT_DIR+x}" = '' ]
[ "${TT_USER_OLD_PATH+x}" = '' ]
[ "${TT_USER_OLD_TARANTOOL_PREFIX+x}" = '' ]
[ "${TT_USER_OLD_TARANTOOL_PREFIX_SET+x}" = '' ]

. "$2"
[ "$PATH" = "$old_path" ]
[ "${TT_CLI_TARANTOOL_PREFIX+x}" = "$old_prefix_set" ]
[ "${TT_CLI_TARANTOOL_PREFIX-}" = "$old_prefix" ]
`

func TestProjectEnvironmentShell(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project with spaces, 'quotes' and $variables")
	require.NoError(t, os.Mkdir(project, 0o755))
	t.Chdir(project)
	require.NoError(t, prepareProjectEnvironment(binary.ScopeProject))

	for _, shell := range []string{"sh", "bash", "zsh"} {
		for _, prefix := range []string{"unset", "empty", "custom"} {
			t.Run(shell+"/"+prefix, func(t *testing.T) {
				shellPath, err := exec.LookPath(shell)
				if err != nil {
					t.Skipf("%s is not installed", shell)
				}

				command := exec.CommandContext(t.Context(), shellPath, "-c",
					projectEnvironmentShellTestScript, "project-environment",
					filepath.Join(project, ".env", "activate.sh"),
					filepath.Join(project, ".env", "deactivate.sh"), prefix, project, shellPath)

				command.Dir = t.TempDir()

				output, err := command.CombinedOutput()
				require.NoError(t, err, string(output))
			})
		}
	}
}

func TestProjectEnvironmentCompletion(t *testing.T) {
	const completionCommand = `#!/bin/sh
case "$*" in
    'completion bash')
        printf '%s\n' '_tt_project_completion() { :; }' \
            'complete -F _tt_project_completion tt'
        ;;
    'completion zsh')
        printf '%s\n' '_tt_project_completion() { :; }' \
            'compdef _tt_project_completion tt'
        ;;
    *) exit 1 ;;
esac
`

	const shellScript = `
set -eu
unset TT_PROJECT_DIR TT_USER_OLD_PATH
unset TT_USER_OLD_TARANTOOL_PREFIX TT_USER_OLD_TARANTOOL_PREFIX_SET
if [ -n "${ZSH_VERSION-}" ]; then
    autoload -Uz compinit
    # Populate the completion table.
    compinit -D -i
fi
PATH=$2
. "$1"
if [ "$3" = available ]; then
    if [ -n "${ZSH_VERSION-}" ]; then
        [ "${_comps[tt]-}" = _tt_project_completion ]
    else
        [ "$(complete -p tt)" = 'complete -F _tt_project_completion tt' ]
    fi
else
    ! command -v _tt_project_completion >/dev/null 2>&1
fi
[ "${_TT_USER_COMPLETION+x}" != x ]
. "$1"
[ "${_TT_USER_COMPLETION+x}" != x ]
`

	for _, shell := range []string{"bash", "zsh"} {
		for _, completion := range []string{"available", "missing", "failed"} {
			t.Run(shell+"/"+completion, func(t *testing.T) {
				shellPath, err := exec.LookPath(shell)
				if err != nil {
					t.Skipf("%s is not installed", shell)
				}

				project := filepath.Join(t.TempDir(), "project with spaces and 'quotes'")
				binDir := filepath.Join(project, ".tarantool", "bin")
				require.NoError(t, os.MkdirAll(binDir, 0o755))
				t.Chdir(project)
				require.NoError(t, prepareProjectEnvironment(binary.ScopeProject))

				if completion != "missing" {
					script := completionCommand
					if completion == "failed" {
						script = "#!/bin/sh\nprintf '%s\\n' 'exit 99'\nexit 1\n"
					}

					require.NoError(t,
						os.WriteFile(filepath.Join(binDir, "tt"), []byte(script), 0o755))
				}

				command := exec.CommandContext(t.Context(), shellPath, "-c", shellScript,
					"project-completion", filepath.Join(project, ".env", "activate.sh"),
					t.TempDir(), completion)
				output, err := command.CombinedOutput()
				require.NoError(t, err, string(output))
			})
		}
	}
}

func TestProjectEnvironmentPreservesScripts(t *testing.T) {
	for _, existing := range []string{"activate.sh", "deactivate.sh"} {
		t.Run(existing, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir(".env", 0o755))

			path := filepath.Join(".env", existing)
			original := []byte("# User-provided environment script.\n")
			require.NoError(t, os.WriteFile(path, original, 0o600))
			require.NoError(t, prepareProjectEnvironment(binary.ScopeProject))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, original, data)

			for _, name := range []string{"activate.sh", "deactivate.sh"} {
				path := filepath.Join(".env", name)
				data, err := os.ReadFile(path)
				require.NoError(t, err)

				data = append(data, []byte("# Keep custom changes.\n")...)
				require.NoError(t, os.WriteFile(path, data, 0o600))
				require.NoError(t, prepareProjectEnvironment(binary.ScopeProject))

				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, data, after)
			}
		})
	}
}

func TestProjectEnvironmentOnlyProjectScope(t *testing.T) {
	for _, scope := range []binary.Scope{binary.ScopeUser, binary.ScopeSystem} {
		t.Run(scope.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, prepareProjectEnvironment(scope))
			require.NoDirExists(t, ".env")
		})
	}
}
