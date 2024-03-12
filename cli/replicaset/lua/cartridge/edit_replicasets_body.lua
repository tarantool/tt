local cartridge = require('cartridge')
local replicasets = ...

local res, err = cartridge.admin_edit_topology({
    replicasets = replicasets
})

assert(res, tostring(err))
