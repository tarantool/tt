-- zigkv: the C functions of the module kv, built by zig with zig-tarantool
-- (its example examples/kv), as functions of box.schema.func.
local M = {}

-- The module kv, which box.schema.func loads for kv.kv_*.
M.path = package.search('kv')
assert(M.path ~= nil, 'the module kv is not found in package.cpath')

-- setup creates the space kv and the functions; box.cfg{} is called before.
function M.setup()
    local space = box.schema.space.create('kv', { if_not_exists = true })
    space:format({ { name = 'key', type = 'string' }, { name = 'value', type = 'string' } })
    space:create_index('pk', { parts = { { 1, 'string' } }, if_not_exists = true })
    for _, name in ipairs({ 'kv_set', 'kv_get', 'kv_del' }) do
        box.schema.func.create('kv.' .. name, { language = 'C', if_not_exists = true })
    end
end

function M.set(key, value)
    return box.func['kv.kv_set']:call({ key, value })
end

function M.get(key)
    return box.func['kv.kv_get']:call({ key })
end

return M
