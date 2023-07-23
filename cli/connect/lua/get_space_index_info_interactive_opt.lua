local yaml = require('yaml')

local input = ...
local space_name, index_name = input[1], input[2]

if box.space[space_name] == nil then
    return yaml.encode({{error="the specified space does not exist"}})
end
if box.space[space_name].index == nil then
    return yaml.encode({{error="there is no indexes for specified space"}})
end
if box.space[space_name].index[index_name] == nil then
    return yaml.encode({{error="the specified index does not exist"}})
end

return yaml.encode({box.space[space_name].index[index_name]})
