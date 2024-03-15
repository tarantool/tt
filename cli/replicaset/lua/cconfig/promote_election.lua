local config = require('config'):get()
local failover = config.replication.failover

if failover ~= 'election' then
    error(('unexpected failover: %q, "election" expected'):format(failover))
end

local election_mode = box.cfg.election_mode
if election_mode ~= 'candidate' and election_mode ~= 'manual' then
    error(('unexpected election_mode: %q, ' .. '"candidate" or "manual" expected'):format(election_mode))
end

box.ctl.promote()
