local yaml = require('yaml')

local function is_not_sys_func(name)
    local sys_names = {
        "box.schema.user.info",
        "LUA",
    }

    if type(name) ~= 'string' then
        return false
    end

    for _, v in pairs(sys_names) do
        if v == name then
            return false
        end
    end

    return true
end

local func_list = unpack(yaml.decode(require('console').eval('box.func')))
local res = {}
for k, v in pairs(func_list) do
    if is_not_sys_func(k) then
        table.insert(res,{func_name = k, language = v.language})
    end
end

return unpack(res)
