local cartridge = require('cartridge')
local fiber = require('fiber')

local bootstrap_function = cartridge.admin_bootstrap_vshard
if bootstrap_function == nil then
    bootstrap_function = require('cartridge.admin').bootstrap_vshard
end

local timeout = ...
local deadline = fiber.time() + timeout
local ok, err

while fiber.time() < deadline do
    ok, err = bootstrap_function()
    if ok then
        break
    end
    if err.err:find("already bootstrapped") then
        error(err.err)
    end
    fiber.sleep(0.1)
end

if err ~= nil then
    err = err.err
end

assert(ok, tostring(err))
