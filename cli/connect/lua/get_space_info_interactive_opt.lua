local yaml = require('yaml')

local space_name = ...

if box.space[space_name] == nil then
    return yaml.encode({{error="the specified space does not exist"}})
end

local space_info = yaml.decode(require('console').eval(
    string.format("box.space.%s", space_name)
))
local res = {}
for k, v in pairs(unpack(space_info)) do
    if k ~= 'index' then
        res[k] = v
    end
end

return yaml.encode({res})
