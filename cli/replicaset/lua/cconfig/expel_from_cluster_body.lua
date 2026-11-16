local uuid = ...

if box.info.ro then
    return false
end

if box.space._cluster.index.uuid:get(uuid) ~= nil then
    box.space._cluster.index.uuid:delete(uuid)
end

return true
