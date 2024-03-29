local function exec(bin, ...)
    local cfg = require("luarocks.core.cfg")
    local util = require("luarocks.util")

    local arg = ...

    -- Tweak help messages.
    if arg == "admin" then
        util.this_program = function(default) -- luacheck: no unused args
            return bin .. " rocks admin"
        end
    else
        util.this_program = function(default) -- luacheck: no unused args
            return bin .. " rocks"
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

    --[[ Disabled: path, upload,
    -- init: luarocks init command generates a project, including local
    dependency management. It also creates two wrapper scripts that can be used to run
    lua & luarocks from inside the project. tt rocks init is unable to create correct luarocks
    wrapper because luarocks script, that must be wrapped, is missing.
    So, rocks init is disabled for now.
    --]]
    local rocks_commands = {
        build = "luarocks.cmd.build",
        config = "luarocks.cmd.config",
        doc = "luarocks.cmd.doc",
        download = "luarocks.cmd.download",
        install = "luarocks.cmd.install",
        lint = "luarocks.cmd.lint",
        list = "luarocks.cmd.list",
        make = "luarocks.cmd.make",
        make_manifest = "luarocks.admin.cmd.make_manifest",
        new_version = "luarocks.cmd.new_version",
        pack = "luarocks.cmd.pack",
        purge = "luarocks.cmd.purge",
        remove = "luarocks.cmd.remove",
        search = "luarocks.cmd.search",
        show = "luarocks.cmd.show",
        test = "luarocks.cmd.test",
        unpack = "luarocks.cmd.unpack",
        which = "luarocks.cmd.which",
        write_rockspec = "luarocks.cmd.write_rockspec",
    }

    cmd.run_command('LuaRocks package manager', rocks_commands, 'luarocks.cmd.external', ...)
end

return {
    exec = exec
}
