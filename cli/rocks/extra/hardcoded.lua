-- This file contains LuaRocks hardcoded settings.

local function get_cwd()
    local cwd = os.getenv("PWD") or io.popen("cd"):read()
    return cwd
end

return {
    PREFIX = [[/usr]],
    LUA_BINDIR = [[/usr/bin]],
    LUA_MODULES_LIB_SUBDIR = [[/lib/tarantool]],
    LUA_MODULES_LUA_SUBDIR = [[/share/tarantool]],
    LUA_INTERPRETER = [[tarantool]],
    ROCKS_SUBDIR = [[/share/tarantool/rocks]],
    ROCKS_SERVERS = {
        [[http://rocks.tarantool.org/]],
    },
    LOCALDIR = get_cwd(),

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
}
