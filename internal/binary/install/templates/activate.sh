#!/bin/sh
if [ "${TT_USER_OLD_PATH+x}" != x ]; then
    export TT_USER_OLD_PATH="${PATH-}"
    export TT_USER_OLD_TARANTOOL_PREFIX_SET="${TT_CLI_TARANTOOL_PREFIX+x}"
    export TT_USER_OLD_TARANTOOL_PREFIX="${TT_CLI_TARANTOOL_PREFIX-}"
fi

export TT_PROJECT_DIR=%s
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
