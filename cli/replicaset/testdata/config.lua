-- Do not set listen for now so connector won't be
-- able to send requests until everything is configured.

box.cfg{
    work_dir = os.getenv("TEST_TNT_WORK_DIR"),
}

box.once("init", function()
    box.schema.user.create('test', {password = 'password'})
    box.schema.user.grant('test', 'execute', 'universe')
end)

rawset(_G, "_TARANTOOL_BACK", _TARANTOOL)
local function set_tarantool_version(version)
    rawset(_G, "_TARANTOOL", version)
end
rawset(_G, 'set_tarantool_version', set_tarantool_version)

local function reset_tarantool_version()
    rawset(_G, "_TARANTOOL", _TARANTOOL_BACK)
end
rawset(_G, 'reset_tarantool_version', reset_tarantool_version)

require("console").listen("unix/:./console.control")
-- Set listen only when every other thing is configured.
box.cfg{
    listen = os.getenv("TEST_TNT_LISTEN"),
}
