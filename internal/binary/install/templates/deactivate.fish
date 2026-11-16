# Restores the environment activate.fish changed: "source .env/deactivate.fish".
if set -q TT_USER_OLD_PATH
    set -gx PATH $TT_USER_OLD_PATH
end

if test "$TT_USER_OLD_TARANTOOL_PREFIX_SET" = x
    set -gx TT_CLI_TARANTOOL_PREFIX "$TT_USER_OLD_TARANTOOL_PREFIX"
else if set -q TT_USER_OLD_TARANTOOL_PREFIX_SET
    set -e TT_CLI_TARANTOOL_PREFIX
end

set -e TT_USER_OLD_PATH
set -e TT_USER_OLD_TARANTOOL_PREFIX
set -e TT_USER_OLD_TARANTOOL_PREFIX_SET
set -e TT_PROJECT_DIR
