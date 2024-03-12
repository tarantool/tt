local cartridge = require('cartridge')

local opts = ...
cartridge.failover_promote(opts.replicaset_leaders, {
    force_inconsistency = opts.force_inconsistency,
    skip_error_on_change = opts.skip_error_on_change,
})
