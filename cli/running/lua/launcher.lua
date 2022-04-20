--- This is a launch script that does the necessary preparation
-- before launching an instance.

local os = require('os')
local console = require('console')
local log = require('log')
local title = require('title')


--- Start an Instance. The "init" file of the Instance passes
-- throught "TT_CLI_INSTANCE".
local function start_instance()
    local instance_path = os.getenv('TT_CLI_INSTANCE')
    if instance_path == nil then
        log.error('Failed to get instance path')
        os.exit(1)
    end
    title.update{
        script_name = instance_path,
        __defer_update = true
    }

    -- Preparation of the "console" socket.
    local console_sock = os.getenv('TT_CLI_CONSOLE_SOCKET')
    if console_sock ~= nil and console_sock ~= '' then
        local cons_listen_sock = console.listen(console_sock)

        -- tarantool 1.10 does not have a trigger on terminate a process.
        -- So the socket will be closed automatically on termination and
        -- deleted from "running.go".
        if box.ctl.on_shutdown ~= nil then
            local close_sock_tr = box.ctl.on_shutdown(function()
                box.ctl.on_shutdown(nil, close_sock_tr)
                local res, err = pcall(cons_listen_sock.close, cons_listen_sock)
                if not res then
                    log.error('Failed to close console socket %s: %s', console_sock, err)
                end
            end)
        end

    end

    -- Start the Instance.
    local success, data = pcall(dofile, instance_path)
    if not success then
        log.error('Failed to run instance: %s', instance_path)
        os.exit(1)
    end
    return 0
end

start_instance()
