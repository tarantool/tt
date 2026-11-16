#!/bin/sh
# Puts the binaries of the project first on PATH: ". .env/activate.sh".
# The project is the directory above the one of this script, found when it
# is sourced, so the project may be moved or copied. A shell that does not
# tell a sourced script its path, such as dash, gets the directory the
# project had when tt wrote the script.
_tt_activate_script=
if [ -n "${BASH_VERSION-}" ]; then
    eval '_tt_activate_script=${BASH_SOURCE[0]-}'
elif [ -n "${ZSH_VERSION-}" ]; then
    eval '_tt_activate_script=${(%%):-%%x}'
fi
if [ -n "$_tt_activate_script" ] &&
    _tt_project_dir=$(CDPATH= cd -- "$(dirname -- "$_tt_activate_script")/.." 2>/dev/null && pwd); then
    :
else
    _tt_project_dir=%s
fi
unset _tt_activate_script

if [ "${TT_USER_OLD_PATH+x}" != x ]; then
    export TT_USER_OLD_PATH="${PATH-}"
    export TT_USER_OLD_TARANTOOL_PREFIX_SET="${TT_CLI_TARANTOOL_PREFIX+x}"
    export TT_USER_OLD_TARANTOOL_PREFIX="${TT_CLI_TARANTOOL_PREFIX-}"
fi

export TT_PROJECT_DIR="$_tt_project_dir"
unset _tt_project_dir
export PATH="$TT_PROJECT_DIR/.tarantool/bin:$TT_USER_OLD_PATH"
export TT_CLI_TARANTOOL_PREFIX="$TT_PROJECT_DIR/.tarantool"

# Enable tt completion for the current shell.
if command -v tt >/dev/null 2>&1; then
    if [ -n "${ZSH_VERSION-}" ] && command -v compdef >/dev/null 2>&1; then
        if _TT_USER_COMPLETION=$(tt completion zsh 2>/dev/null); then
            eval "$_TT_USER_COMPLETION"
        fi
    elif [ -n "${BASH_VERSION-}" ]; then
        if _TT_USER_COMPLETION=$(tt completion bash 2>/dev/null); then
            eval "$_TT_USER_COMPLETION"
        fi
    fi
    unset _TT_USER_COMPLETION
fi
