-- This is a router listening network socket.

local cfg = require('localcfg')
cfg.discovery_mode = 'off'

-- Start the database with sharding
local vshard = require('vshard')
vshard.router.cfg(cfg)
vshard.router.bootstrap()

local crud = require('crud')
crud.init_router()

box.schema.user.grant('guest', 'read,write,execute', 'universe', nil, {if_not_exists = true})

