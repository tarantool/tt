local yaml = require('yaml')

local function no_added_such_name(res, name)
    for _,v in pairs(res) do
        if v.name == name then
            return false
        end
    end

    return true
end

local space_name = ...
if box.space[space_name] == nil then
    return yaml.encode({{error="the specified space does not exist"}})
end

local indexes_list = box.space[space_name].index

if indexes_list == nil then
    return yaml.encode({{error="the specified space does not have indexes"}})
end

local res = {}
for _, v in pairs(indexes_list) do
    if no_added_such_name(res, v.name) then
        table.insert(res, {type = v.type, name = v.name})
    end
end

return yaml.encode(res)
