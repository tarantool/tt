local cartridge = require('cartridge')

local function format_topology(replicaset)
    local instances = {}
    for _, server in pairs(replicaset.servers) do
        local instance = {
            alias = server.alias,
            uuid = server.uuid,
            uri = server.uri,
        }
        table.insert(instances, instance)
    end

    local leader_uuid
    if replicaset.active_master ~= nil then
        leader_uuid = replicaset.active_master.uuid
    end

    local topology_replicaset = {
        uuid = replicaset.uuid,
        leaderuuid = leader_uuid,
        alias = replicaset.alias,
        roles = replicaset.roles,
        vshard_group = replicaset.vshard_group,
        instances = instances,
    }

    return topology_replicaset
end

local topology_replicasets = {}

local replicasets, err = cartridge.admin_get_replicasets()

if err ~= nil then
    err = err.err
end

assert(err == nil, tostring(err))

for _, replicaset in pairs(replicasets) do
    local topology_replicaset = format_topology(replicaset)
    table.insert(topology_replicasets, topology_replicaset)
end

local failover_params = require('cartridge').failover_get_params()
local issues = require('cartridge.issues').list_on_cluster()
local is_critical = false

if type(box.cfg) ~= 'function' then
    local uuid = box.info().uuid

    for _, issue in pairs(issues) do
        if issue.level == 'critical' and issue.instance_uuid == uuid then
            is_critical = true
            break
        end
    end
end

return {
    failover = failover_params.mode,
    provider = failover_params.state_provider or "none",
    replicasets = topology_replicasets,
    is_critical = is_critical,
}
