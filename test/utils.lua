-- This module contains auxiliary functions for integration tests that use lua code.
-- To use this module, you need to copy this file to the temp pytest directory for the duration
-- of the integration tests, then use the lua require function in your instance cfg file.
-- The copying of this file to pytest temdir is already implemented in class
-- TarantoolTestInstance of test/utils.py module.
-- Just use require('utils').bind_free_port(arg[0]) inside your cfg instance file.
-- If you need to get the port value in the lua test instance cfg file,
-- use require('utils').get_bound_port() after require('utils').bind_free_port(arg[0]).

local box    = require('box')
local ffi    = require('ffi')
local socket = require('socket')

local testutils = {}

function testutils.get_bound_address_old_tarantool()
    -- Get bound via box.cfg({listen = '127.0.0.1:0'}) socket addr for tarantool major version 1.
    -- For tarantool major version higher then 1, this function can be replaced by box.info.listen.
    -- Table res contains listening and client sockets.
    local res = {}
    for fd = 0, 65535 do
        local addrinfo = socket.internal.name(fd)
        -- Assume that the socket listens on 127.0.0.1.
        local is_matched = addrinfo ~= nil and addrinfo.host == '127.0.0.1' and
            addrinfo.family == 'AF_INET' and addrinfo.type == 'SOCK_STREAM' and
            addrinfo.protocol == 'tcp' and type(addrinfo.port) == 'number'
        if is_matched then
            addrinfo.fd = fd
            table.insert(res, addrinfo)
        end
    end

    -- Table l_res contains listening sockets.
    local l_res = {}
    -- We need only listening, not client sockets.
    -- Filters sockets with SO_REUSEADDR.
    for _, sock in pairs(res) do
        local value  = ffi.new('int[1]')
        local len    = ffi.new('size_t[1]', ffi.sizeof('int'))
        local level  = socket.internal.SOL_SOCKET
        local status = ffi.C.getsockopt(
            sock.fd,
            level,
            socket.internal.SO_OPT[level].SO_REUSEADDR.iname,
            value, len
        )
        if status ~= 0 then
            error('problem with calling getsockopt() function')
        end
        if value[0] > 0 then
            table.insert(l_res, sock)
        end
    end

    -- If there are several listening sockets, we don't know which one is iproto's one.
    if #l_res ~= 1 then
        error(('zero or more than one listening TCP sockets: %d'):format(#l_res))
    end

    return ('%s:%s'):format(l_res[1].host, l_res[1].port)
end

function testutils.get_bound_port()
    -- Returns bound port.
    -- Can be used after call testutils.bind_free_port() if you need to get the port value
    -- in the lua test instance cfg file.
    local address = box.info.listen
    if address == nil then
        -- In case of tarantool major version 1.
        address = testutils.get_bound_address_old_tarantool()
    end
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
