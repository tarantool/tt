-- This is a router listening network socket.

local vshard = require('vshard')

-- Start the database with sharding
box.cfg{
    listen = 3300,
    replication_connect_quorum = 0,
}

local cfg = require('localcfg')
vshard.router.cfg(cfg)
vshard.router.bootstrap()

local crud = require('crud')
crud.init_router()

box.schema.user.grant('guest', 'read,write,execute', 'universe', nil, {if_not_exists = true})

