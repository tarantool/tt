-- This file contains LuaRocks hardcoded settings.

local function get_tarantool_path()
    local path = os.getenv('TT_CLI_TARANTOOL_PATH')
    if path ~= nil then
        return path
    end

    return "/usr/bin"
end

local function get_tarantool_include_path()
    local path = os.getenv('TT_CLI_TARANTOOL_INCLUDE')
    if path ~= nil then
        return path
    end

    return "/usr/include/tarantool"
end

local function get_tarantool_prefix_path()
    local path = os.getenv('TT_CLI_TARANTOOL_PREFIX')
    if path ~= nil then
        return path
    end

    return "/usr"
end

local cwd = tt_getwd()

-- The platform comes from the Go runtime of tt and not from
-- luarocks.core.sysdetect: it reads the CPU of /bin/sh and takes the first
-- slice of a fat binary. /bin/sh of macOS is a fat binary whose first slice
-- is x86_64, so on Apple Silicon LuaRocks would take the host for
-- macosx-x86_64.
local system, processor = tt_host_platform()

return {
    SYSTEM = system,
    PROCESSOR = processor,
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
