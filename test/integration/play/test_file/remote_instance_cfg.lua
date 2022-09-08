-- This is a script that configures the remote instance to which .xlog data
-- will be transferred for testing tt play command.
-- A test space 'tester' is being created, the data about which
-- will be present in the transmitted .xlog file during testing tt play.

local box = require('box')

local function configure_instance()
    box.cfg{listen = 3301}
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
