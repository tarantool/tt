-- This is a script that configures the remote instance to which .xlog data
-- will be transferred for testing tt play command.
-- A test space 'tester' is being created, the data about which
-- will be present in the transmitted .xlog file during testing tt play.
-- Call require('utils').bind_free_port(arg[0]) is required for using
-- TarantoolTestInstance class of test/utils.py.

local box = require('box')
-- The module below should be in a pytest temporary directory.
local testutils = require('utils')

local function configure_instance()
    testutils.bind_free_port(arg[0]) -- arg[0] is 'remote_instance_cfg.lua'
    local tester = box.schema.space.create('tester', {id = 999})
    tester:format(
        {
            {name = 'id', type = 'unsigned'},
            {name = 'band_name', type = 'string'},
            {name = 'year', type = 'unsigned'}
        }
    )
    tester:create_index('primary', {type = 'tree', parts = {'id'}})
    box.schema.user.grant('guest', 'read,write', 'space', 'tester')
end

configure_instance()
