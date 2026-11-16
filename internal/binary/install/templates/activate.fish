# Puts the binaries of the project first on PATH in fish:
# "source .env/activate.fish". The project is the directory above the one
# of this script, found when it is sourced, so the project may be moved or
# copied.
if not set -q TT_USER_OLD_PATH
    set -gx TT_USER_OLD_PATH $PATH
    if set -q TT_CLI_TARANTOOL_PREFIX
        set -gx TT_USER_OLD_TARANTOOL_PREFIX_SET x
    else
        set -gx TT_USER_OLD_TARANTOOL_PREFIX_SET ""
    end
    set -gx TT_USER_OLD_TARANTOOL_PREFIX "$TT_CLI_TARANTOOL_PREFIX"
end

set -gx TT_PROJECT_DIR (builtin realpath (dirname (status --current-filename))/..)
set -gx PATH "$TT_PROJECT_DIR/.tarantool/bin" $TT_USER_OLD_PATH
set -gx TT_CLI_TARANTOOL_PREFIX "$TT_PROJECT_DIR/.tarantool"

# Enable tt completion for the current shell.
if command -q tt
    tt completion fish 2>/dev/null | source
end
