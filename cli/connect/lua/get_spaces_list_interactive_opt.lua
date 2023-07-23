local yaml = require('yaml')

local space_list = box.space

local res = {}
for k, v in pairs(space_list) do
    if type(k) == 'string' and not k:match("^_") then
        table.insert(res, {engine = v.engine, space_name = k})
    end
end

return yaml.encode(res)
