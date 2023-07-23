local yaml = require('yaml')

local space_name = ...

if box.space[space_name] == nil then
    return yaml.encode({{error="the specified space does not exist"}})
end

return yaml.encode(box.space[space_name]:format())
