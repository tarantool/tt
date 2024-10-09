local ok, vshard = pcall(require, 'vshard')
if not ok then
    error("failed to require vshard module")
end
local fiber = require('fiber')
local config = require('config')

local is_router = false
for _, role in ipairs(config:get().sharding.roles) do
    if role == "router" then
        is_router = true
        break
    end
end

if not is_router then
    error("instance must be a router to bootstrap vshard")
end

pcall(vshard.router.master_search_wakeup)

local timeout = ...
local deadline = fiber.time() + timeout
local ok, err

while fiber.time() < deadline do
    ok, err = vshard.router.bootstrap({timeout = timeout})
    if ok then
        break
    end
    if err.message:find("already bootstrapped") then
        error(err.message)
    end
    fiber.sleep(0.1)
end

assert(ok, tostring(err))
