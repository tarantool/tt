local config = require('config')
local fiber = require('fiber')
local fio = require('fio')
local log = require('log')
local wasm = require('wasm')

box.schema.space.create('notes', {
    if_not_exists = true,
    format = {
        {name = 'id', type = 'unsigned'},
        {name = 'text', type = 'string'},
    },
})
box.space.notes:create_index('primary', {if_not_exists = true})

-- app.cfg.listen of config.yaml: where the HTTP API listens.
local listen = (config:get('app.cfg') or {}).listen or '127.0.0.1:8080'
local host, port = listen:match('^(.+):(%d+)$')

-- The router is a file of this package, next to this module.
local router = wasm.load(fio.pathjoin(debug.sourcedir(), '{{.Output}}'), {
    args = {host, port},
    inherit_env = false,
    -- The addresses the component may use. The check applies to the peers
    -- of the connections it accepts as well, so it names no ports: a client
    -- connects from a port of its own.
    allowed_ips = {host},
})

-- The router serves on a thread of its own; Tarantool stops it on shutdown.
local server = wasm.run(router)
log.info('notes: HTTP API on http://%s', listen)

fiber.create(function()
    local ok, err = pcall(wasm.join, server)
    log.info('notes: HTTP API stopped: %s', ok and 'done' or err)
end)

return {}
