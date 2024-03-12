local cartridge = require('cartridge')
local fiber = require('fiber')

local timeout = ...

local deadline = fiber.time() + timeout
while fiber.time() < deadline do
    if cartridge.is_healthy() then
        return
    end
    fiber.sleep(0.01)
end

error("timeout")
