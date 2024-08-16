local ok, config = pcall(require, 'config')

-- Default configuration in case the Tarantool 3.x config module is unavailable.
local config_info_full = {
    status = "uninitialized",
    alerts = {},
}

-- The type check for 'config.info' ensures that we are working with the correct
-- Tarantool 3.x config module. This is necessary because another module,
-- 'moonlibs/config', shares the same name.
if ok and config ~= nil and type(config) == "table" and type(config.info) == "function" then
    config_info_full = config:info()
end

local config_info = {
    status = config_info_full.status,
    alerts = {}
}

-- This loop extracts only the 'type' and 'message' fields from each alert,
-- discarding any additional fields that might be present in the config.alerts.
-- We only need the type of the error and its message for the status report,
-- as other fields are considered extra and unnecessary for our purposes.
for _, alert in ipairs(config_info_full.alerts) do
    table.insert(config_info.alerts, {
        type = alert.type,
        message = alert.message
    })
end

local ok, box_replication = pcall(function() return box.info.replication end)
if not ok or box_replication == nil then
    box_replication = {}
end

-- We collect information only for instances that have an upstream,
-- as this field is necessary for subsequent error reporting
local replication_info = {}
for _, instance in pairs(box_replication) do
    if instance.upstream ~= nil then
        table.insert(replication_info, {
            uuid = instance.uuid,
            name = instance.name,
            upstream = instance.upstream and {
                status = instance.upstream.status or "--",
                message = instance.upstream.message
            }
        })
    end
end

local ok, read_only = pcall(function() return box.info.ro end)
if ok then
    read_only = read_only and "RO" or "RW"
else
    read_only = "RO"
end

local ok, box_status = pcall(function() return box.info.status end)
if not ok then
    box_status = "N/A"
end

local ok, uuid = pcall(function() return box.info.uuid end)
if not ok then
    uuid = "00000000-0000-0000-0000-000000000000"
end

return {
    replication_info = replication_info,
    config_info = config_info,
    read_only = read_only,
    box_status = box_status,
    uuid = uuid,
}
