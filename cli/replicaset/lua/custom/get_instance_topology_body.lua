local luri = require('uri')

local box_info = box.info()
local replicaset_info = box_info.replicaset
if replicaset_info == nil then
    replicaset_info = box_info.cluster
end
local leader_uuid = nil
if box_info.election ~= nil then
    local id = box_info.election.leader
    if id ~= 0 then
        leader_uuid = box_info.replication[id].uuid
    end
end
local replicaset = {
    uuid  = replicaset_info.uuid,
    alias = replicaset_info.name,
    leaderuuid = leader_uuid,
    instances = {},
    instanceuuid = box_info.uuid,
    instancerw = box.cfg.read_only == false,
}

for _, instance in ipairs(box_info.replication) do
    local uri = nil
    if instance.upstream ~= nil then
        uri = instance.upstream.peer
    elseif box.cfg.listen ~= nil then
        if type(box.cfg.listen) == 'string' then
            uri = box.cfg.listen
        elseif #box.cfg.listen > 0 then
            uri = box.cfg.listen[1].uri
        end
    end

    if uri ~= nil then
        local ok, parsed = pcall(luri.parse, uri)
        if ok then
            parsed.login = nil
            parsed.password = nil
            uri = luri.format(parsed)
        else
            uri = nil
        end
    end

    table.insert(replicaset.instances, {
        alias = instance.name,
        uuid  = instance.uuid,
        uri   = uri,
    })
end

return replicaset
