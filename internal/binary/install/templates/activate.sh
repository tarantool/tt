#!/bin/sh
if [ "${TT_USER_OLD_PATH+x}" != x ]; then
    export TT_USER_OLD_PATH="${PATH-}"
    export TT_USER_OLD_TARANTOOL_PREFIX_SET="${TT_CLI_TARANTOOL_PREFIX+x}"
    export TT_USER_OLD_TARANTOOL_PREFIX="${TT_CLI_TARANTOOL_PREFIX-}"
fi

export TT_PROJECT_DIR=%s
export PATH="$TT_PROJECT_DIR/.tarantool/bin:$TT_USER_OLD_PATH"
export TT_CLI_TARANTOOL_PREFIX="$TT_PROJECT_DIR/.tarantool"
