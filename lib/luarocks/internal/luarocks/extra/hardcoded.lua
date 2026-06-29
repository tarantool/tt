-- This file contains LuaRocks hardcoded settings.
-- Adapted from tt/cli/rocks/extra/hardcoded.lua. Env var names align with
-- upstream LuaRocks conventions; the go-luarocks lua engine serves them via a
-- custom os.getenv backed by a Go map populated from Config.Tarantool (IV7).

local function get_tarantool_path()
    return os.getenv('LUA_BINDIR') or "/usr/bin"
end

local function get_tarantool_include_path()
    return os.getenv('LUA_INCDIR') or "/usr/include/tarantool"
end

local function get_tarantool_prefix_path()
    return os.getenv('LUAROCKS_PREFIX') or "/usr"
end

local cwd = glr_getwd()

return {
    PREFIX = get_tarantool_prefix_path(),
    LUA_BINDIR = get_tarantool_path(),
    LUA_INCDIR = get_tarantool_include_path(),
    LUA_MODULES_LIB_SUBDIR = [[/lib/tarantool]],
    LUA_MODULES_LUA_SUBDIR = [[/share/tarantool]],
    LUA_INTERPRETER = [[tarantool]],
    ROCKS_SUBDIR = [[/share/tarantool/rocks]],
    ROCKS_SERVERS = {
        [[http://rocks.tarantool.org/]],
    },
    LOCALDIR = cwd,

    HOME_TREE_SUBDIR = [[/.rocks]],
    EXTERNAL_DEPS_SUBDIRS = {
        bin = "bin",
        lib = {"lib", [[lib64]]},
        include = "include",
    },
    RUNTIME_EXTERNAL_DEPS_SUBDIRS = {
        bin = "bin",
        lib = {"lib", [[lib64]]},
        include = "include",
    },
    LOCAL_BY_DEFAULT = true,
    FORCE_HARDCODED = true,
}
