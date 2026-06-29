local function exec(bin, ...)
    local cfg = require("luarocks.core.cfg")
    local util = require("luarocks.util")

    local arg = ...

    -- Tweak help messages.
    if arg == "admin" then
        util.this_program = function(default) -- luacheck: no unused args
            return bin .. " admin"
        end
    else
        util.this_program = function(default) -- luacheck: no unused args
            return bin
        end
    end
    local cmd = require("luarocks.cmd")

    if arg == "admin" then
        local description = "LuaRocks repository administration interface"
        local admin_args = {...}

        table.remove(admin_args, 1)

        local commands = {
           make_manifest = "luarocks.admin.cmd.make_manifest",
           add = "luarocks.admin.cmd.add",
           remove = "luarocks.admin.cmd.remove",
           refresh_cache = "luarocks.admin.cmd.refresh_cache",
        }

        cmd.run_command(description, commands, "luarocks.admin.cmd.external", unpack(admin_args))
        return
    end

    local rocks_commands = {
        build = "luarocks.cmd.build",
        config = "luarocks.cmd.config",
        doc = "luarocks.cmd.doc",
        download = "luarocks.cmd.download",
        init = "luarocks.cmd.init",
        install = "luarocks.cmd.install",
        lint = "luarocks.cmd.lint",
        list = "luarocks.cmd.list",
        make = "luarocks.cmd.make",
        make_manifest = "luarocks.admin.cmd.make_manifest",
        new_version = "luarocks.cmd.new_version",
        pack = "luarocks.cmd.pack",
        path = "luarocks.cmd.path",
        purge = "luarocks.cmd.purge",
        remove = "luarocks.cmd.remove",
        search = "luarocks.cmd.search",
        show = "luarocks.cmd.show",
        test = "luarocks.cmd.test",
        unpack = "luarocks.cmd.unpack",
        upload = "luarocks.cmd.upload",
        which = "luarocks.cmd.which",
        write_rockspec = "luarocks.cmd.write_rockspec",
    }

    cmd.run_command('LuaRocks package manager', rocks_commands, 'luarocks.cmd.external', ...)
end

return {
    exec = exec
}
