local name = ...

local info = box.info
local peer = {
    name = info.name,
    replicaset = info.replicaset.name,
}

for _, replica in pairs(info.replication) do
    if replica.name == name and replica.uuid ~= info.uuid then
        peer.uuid = replica.uuid
        if replica.upstream ~= nil then
            peer.upstream = replica.upstream.status
        end
        if replica.downstream ~= nil then
            peer.downstream = replica.downstream.status
        end
    end
end

if peer.uuid == nil and box.space._cluster ~= nil then
    for _, tuple in box.space._cluster:pairs() do
        if tuple[3] == name and tuple[2] ~= info.uuid then
            peer.uuid = tuple[2]
        end
    end
end

return peer
