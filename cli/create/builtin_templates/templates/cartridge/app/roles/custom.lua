local cartridge = require('cartridge')

-- init is a role initialization function.
-- Can be used to create spaces, indexes, grant permissions, etc.
local function init(opts) -- luacheck: no unused args
    -- if opts.is_master then
    -- end

    local httpd = assert(cartridge.service_get('httpd'), "Failed to get httpd service")
    httpd:route({method = 'GET', path = '/hello'}, function()
        return {body = 'Hello world!'}
    end)

    return true
end

-- stop is a role termination function.
local function stop()
    return true
end

-- validate_config validates role configuration.
local function validate_config(conf_new, conf_old) -- luacheck: no unused args
    return true
end

-- apply_config applies role configuration.
local function apply_config(conf, opts) -- luacheck: no unused args
    -- if opts.is_master then
    -- end

    return true
end

return {
    role_name = 'app.roles.custom',
    init = init,
    stop = stop,
    validate_config = validate_config,
    apply_config = apply_config,
    -- dependencies = {'cartridge.roles.vshard-router'},
}
