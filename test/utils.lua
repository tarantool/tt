-- This module contains auxiliary functions for integration tests that use lua code.
-- To use this module, you need to copy this file to the temp pytest directory for the duration
-- of the integration tests, then use the lua require function in your instance cfg file.
-- The copying of this file to pytest temdir is already implemented in class
-- TarantoolTestInstance of test/utils.py module.
-- Just use require('utils').bind_free_port(arg[0]) inside your cfg instance file.
-- If you need to get the port value in the lua test instance cfg file,
-- use require('utils').get_bound_port() after require('utils').bind_free_port(arg[0]).

local box    = require('box')

local testutils = {}

function testutils.get_bound_port()
    -- Returns bound port.
    -- Can be used after call testutils.bind_free_port() if you need to get the port value
    -- in the lua test instance cfg file.
    local address = box.info.listen
    -- Get port from address string '127.0.0.1:*'.
    local port = address:match(':(.*)')

    if port == nil then
        error('unable to get bound port, perhaps forgot to call testutils.bind_free_port()')
    end

    return port
end

function testutils.dump_bound_port(instance_file_name)
    -- Dump bound port to file for pytest.
    -- It will be regular text file which name is 'instance_file_name.port'.
    local file, err = io.open(instance_file_name .. '.port', 'w')
    if file == nil then
        error(err)
    end

    local port = testutils.get_bound_port()
    file:write(port)
    file:close()
end

function testutils.bind_free_port(instance_file_name)
    -- Bind free port and save it to file for pytest.
    box.cfg({listen = '127.0.0.1:0'})
    testutils.dump_bound_port(instance_file_name)
end

return testutils
