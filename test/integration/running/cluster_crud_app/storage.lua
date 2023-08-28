local fio = require('fio')
local log = require('log')

local localcfg = require('localcfg')
local replicasets = {'cbf06940-0790-498b-948d-042b62cf3d29',
               'ac522f65-aa94-4134-9f64-51ee384f1a54'}

-- Start the database with sharding
vshard = require('vshard')
vshard.storage.cfg(localcfg, box.cfg.instance_uuid)

local crud = require 'crud'
crud.init_storage()

-- box.schema.user.grant('storage', 'read,write,execute', 'universe', nil, {if_not_exists = true})

local user = box.schema.space.create('user', { if_not_exists = true })
user:format({
    {'id', 'unsigned'},
    {'bucket_id', 'unsigned'},
    {'name', 'string'},
    {'second_name', 'string', is_nullable = true},
    {'age', 'unsigned', is_nullable = false},
})
user:create_index('id', {parts = {'id'}, if_not_exists = true})
user:create_index('bucket_id', {parts = {'bucket_id'}, unique = false, if_not_exists = true})
