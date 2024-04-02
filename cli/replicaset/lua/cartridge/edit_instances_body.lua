local cartridge = require('cartridge')
local servers = ...

local res, err = cartridge.admin_edit_topology({
    servers = servers,
})

assert(res, tostring(err))
