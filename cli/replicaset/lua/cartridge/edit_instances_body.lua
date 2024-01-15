local cartridge = require('cartridge')

local servers = ...
local res, err = cartridge.admin_edit_topology({
    servers = servers,
})

if err ~= nil then
    err = err.err
end

assert(err == nil, tostring(err))
